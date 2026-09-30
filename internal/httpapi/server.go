// Package httpapi serves the REST API, the WebSocket call channel and the web UI.
package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/config"
	"github.com/revocx35/web-ip-phone/internal/phone"
	"github.com/revocx35/web-ip-phone/internal/secretbox"
	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
)

// SIPTools is what the admin/user APIs need from the SIP engine.
type SIPTools interface {
	CheckCredentials(ctx context.Context, a *sipua.Account) error
	Ping(ctx context.Context, p *sipua.PBX) (time.Duration, int, error)
}

type Server struct {
	cfg     *config.Config
	store   *store.Store
	box     *secretbox.Box
	svc     *phone.Service
	sip     SIPTools
	log     *slog.Logger
	static  fs.FS
	version string
	started time.Time
	// CertFingerprint is shown to admins so app users can verify the TLS certificate.
	CertFingerprint string

	pwParams auth.Params

	setupMu    sync.Mutex
	setupToken string // "" once an admin exists

	loginThrottle *auth.Throttle
	mfaThrottle   *auth.Throttle
	setupThrottle *auth.Throttle
	apiLimiter      *auth.RateLimiter
	wsLimiter       *auth.RateLimiter
	sipCheckLimiter *auth.RateLimiter

	mfaMu      sync.Mutex
	mfaTickets map[string]*mfaTicket
	now        func() time.Time
}

type Options struct {
	Config     *config.Config
	Store      *store.Store
	Box        *secretbox.Box
	Service    *phone.Service
	SIP        SIPTools
	Logger     *slog.Logger
	Static     fs.FS
	Version    string
	SetupToken string
	PWParams   *auth.Params
}

func New(o Options) *Server {
	s := &Server{
		cfg: o.Config, store: o.Store, box: o.Box, svc: o.Service, sip: o.SIP, log: o.Logger.With("component", "http"),
		static: o.Static, version: o.Version, started: time.Now(), setupToken: o.SetupToken, pwParams: auth.DefaultParams,
		// 5 free failures, then 2 s doubling up to 15 min; forgotten after 1 h without failures.
		loginThrottle: auth.NewThrottle(5, 2*time.Second, 15*time.Minute, time.Hour),
		mfaThrottle:   auth.NewThrottle(5, 5*time.Second, 15*time.Minute, time.Hour),
		setupThrottle: auth.NewThrottle(5, 5*time.Second, 30*time.Minute, time.Hour),
		apiLimiter:    auth.NewRateLimiter(600, 120),
		wsLimiter:     auth.NewRateLimiter(30, 10),
		// SIP credential checks per user: 10 per hour, burst 5.
		sipCheckLimiter: auth.NewRateLimiterPer(10, time.Hour, 5),
		mfaTickets:    map[string]*mfaTicket{},
		now:           time.Now,
	}
	if o.PWParams != nil {
		s.pwParams = *o.PWParams
	}
	return s
}

// SetupPending reports whether the first admin still has to be created.
func (s *Server) SetupPending() bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupToken != ""
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h func(http.ResponseWriter, *http.Request) error, mw ...func(handlerFunc) handlerFunc) {
		f := handlerFunc(h)
		for i := len(mw) - 1; i >= 0; i-- {
			f = mw[i](f)
		}
		mux.Handle(pattern, s.errorHandler(f))
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok\n"))
	})

	// public
	api("GET /api/v1/info", s.handleInfo)
	api("GET /api/v1/setup", s.handleSetupStatus)
	api("POST /api/v1/setup", s.handleSetup, s.csrf)
	api("POST /api/v1/auth/login", s.handleLogin, s.csrf)
	api("POST /api/v1/auth/mfa", s.handleLoginMFA, s.csrf)

	// signed in (restricted sessions may only reach these: must change password / enrol 2FA)
	api("POST /api/v1/auth/logout", s.handleLogout, s.authed(allowRestricted), s.csrf)
	api("GET /api/v1/me", s.handleMe, s.authed(allowRestricted))
	api("POST /api/v1/me/password", s.handleChangePassword, s.authed(allowRestricted), s.csrf)
	api("POST /api/v1/me/totp/setup", s.handleTOTPSetup, s.authed(allowRestricted), s.csrf)
	api("POST /api/v1/me/totp/enable", s.handleTOTPEnable, s.authed(allowRestricted), s.csrf)
	api("POST /api/v1/me/totp/disable", s.handleTOTPDisable, s.authed(0), s.csrf)
	api("POST /api/v1/me/totp/recovery", s.handleTOTPRecovery, s.authed(0), s.csrf)
	api("GET /api/v1/me/sessions", s.handleSessions, s.authed(0))
	api("DELETE /api/v1/me/sessions/{id}", s.handleDeleteSession, s.authed(0), s.csrf)
	api("PATCH /api/v1/me", s.handleUpdateMe, s.authed(0), s.csrf)

	api("GET /api/v1/phones", s.handlePhones, s.authed(0))
	api("POST /api/v1/phones", s.handleCreatePhone, s.authed(0), s.csrf)
	api("PUT /api/v1/phones/{id}", s.handleUpdatePhone, s.authed(0), s.csrf)
	api("DELETE /api/v1/phones/{id}", s.handleDeletePhone, s.authed(0), s.csrf)
	api("POST /api/v1/phones/{id}/test", s.handleTestPhone, s.authed(0), s.csrf)
	api("GET /api/v1/calls", s.handleCalls, s.authed(0))
	api("DELETE /api/v1/calls", s.handleClearCalls, s.authed(0), s.csrf)
	mux.Handle("GET /api/v1/ws", s.errorHandler(s.authed(0)(s.handleWS)))

	// admin
	ad := func(pattern string, h func(http.ResponseWriter, *http.Request) error) {
		if strings.HasPrefix(pattern, "GET ") {
			api(pattern, h, s.authed(needAdmin))
		} else {
			api(pattern, h, s.authed(needAdmin), s.csrf)
		}
	}
	ad("GET /api/v1/admin/users", s.handleAdminUsers)
	ad("POST /api/v1/admin/users", s.handleAdminCreateUser)
	ad("PATCH /api/v1/admin/users/{id}", s.handleAdminUpdateUser)
	ad("DELETE /api/v1/admin/users/{id}", s.handleAdminDeleteUser)
	ad("POST /api/v1/admin/users/{id}/password", s.handleAdminSetPassword)
	ad("POST /api/v1/admin/users/{id}/reset-2fa", s.handleAdminReset2FA)
	ad("DELETE /api/v1/admin/users/{id}/sessions", s.handleAdminRevokeSessions)
	ad("GET /api/v1/admin/users/{id}/access", s.handleAdminGetAccess)
	ad("PUT /api/v1/admin/users/{id}/access", s.handleAdminSetAccess)
	ad("GET /api/v1/admin/pbxs", s.handleAdminPBXs)
	ad("POST /api/v1/admin/pbxs", s.handleAdminCreatePBX)
	ad("PUT /api/v1/admin/pbxs/{id}", s.handleAdminUpdatePBX)
	ad("DELETE /api/v1/admin/pbxs/{id}", s.handleAdminDeletePBX)
	ad("POST /api/v1/admin/pbxs/{id}/ping", s.handleAdminPingPBX)
	ad("GET /api/v1/admin/phones", s.handleAdminPhones)
	ad("POST /api/v1/admin/pbxs/{id}/phones", s.handleAdminCreatePhone)
	ad("PUT /api/v1/admin/phones/{id}", s.handleAdminUpdatePhone)
	ad("DELETE /api/v1/admin/phones/{id}", s.handleAdminDeletePhone)
	ad("POST /api/v1/admin/phones/{id}/test", s.handleAdminTestPhone)
	ad("GET /api/v1/admin/status", s.handleAdminStatus)
	ad("DELETE /api/v1/admin/active-calls/{id}", s.handleAdminHangup)
	ad("GET /api/v1/admin/calls", s.handleAdminCalls)
	ad("GET /api/v1/admin/audit", s.handleAdminAudit)
	ad("GET /api/v1/admin/settings", s.handleAdminGetSettings)
	ad("PUT /api/v1/admin/settings", s.handleAdminPutSettings)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found", "code": "not_found"})
	})
	mux.Handle("/", s.spa())

	return s.base(mux)
}

type handlerFunc func(http.ResponseWriter, *http.Request) error

func (s *Server) errorHandler(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			s.writeErr(w, r, err)
		}
	})
}

// cspPolicy allows only same-origin code. AudioWorklet modules and the WebSocket are
// same-origin too; data: images are the 2FA QR codes.
const cspPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; media-src 'self'; worker-src 'self'; font-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'; manifest-src 'self'"

// base resolves the client, applies security headers and the global API rate limit.
func (s *Server) base(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ci := s.resolveClient(r)
		r = r.WithContext(context.WithValue(r.Context(), keyClient, ci))
		h := w.Header()
		h.Set("Content-Security-Policy", cspPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "microphone=(self), camera=(), geolocation=(), payment=(), usb=()")
		if ci.HTTPS {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.apiLimiter.Allow(ci.IP.String()) {
			h.Set("Retry-After", "10")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests", "code": "rate_limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrf protects cookie-authenticated state changes. Browsers never send custom headers
// cross-site without a CORS preflight (which this server never grants), and the Origin /
// Sec-Fetch-Site checks stop the rest. Bearer-token requests (native app) are not CSRF-able.
func (s *Server) csrf(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			return next(w, r)
		}
		if r.Header.Get("X-Requested-With") != "webphone" {
			return &apiError{status: http.StatusForbidden, code: "csrf", msg: "missing X-Requested-With header"}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			return &apiError{status: http.StatusForbidden, code: "csrf", msg: "cross-site request refused"}
		}
		if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(r, o) {
			return &apiError{status: http.StatusForbidden, code: "csrf", msg: "cross-origin request refused"}
		}
		return next(w, r)
	}
}

// spa serves the embedded web UI; unknown paths get index.html (client-side routing).
func (s *Server) spa() http.Handler {
	fileServer := http.FileServerFS(s.static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p != "" {
			if st, err := fs.Stat(s.static, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(s.static, "index.html")
		if err != nil {
			http.Error(w, "web UI not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{"name": "Web IP Phone", "version": s.version, "setupRequired": s.SetupPending(), "api": 1})
	return nil
}
