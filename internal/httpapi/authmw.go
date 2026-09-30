package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/store"
)

type authCtx struct {
	User     *store.User
	Session  *store.Session
	Bearer   bool
	Settings store.Settings
	// Restricted sessions may only change the password / enrol 2FA / sign out.
	MustChangePassword bool
	MustEnroll2FA      bool
}

func (a *authCtx) restricted() bool { return a.MustChangePassword || a.MustEnroll2FA }

const (
	allowRestricted = 1 << iota
	needAdmin
)

func authOf(r *http.Request) *authCtx {
	a, _ := r.Context().Value(keyAuth).(*authCtx)
	return a
}

func cookieName(https bool) string {
	if https {
		return "__Host-wip_session"
	}
	return "wip_session"
}

func requires2FA(st store.Settings, u *store.User) bool {
	return st.Require2FA == "all" || (st.Require2FA == "admins" && u.IsAdmin)
}

// loadAuth resolves the session of a request, or returns nil.
func (s *Server) loadAuth(r *http.Request) (*authCtx, error) {
	var token string
	bearer := false
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token, bearer = strings.TrimSpace(h[len("Bearer "):]), true
	} else if c, err := r.Cookie(cookieName(client(r).HTTPS)); err == nil {
		token = c.Value
	}
	if token == "" || !auth.ValidTokenFormat(token) {
		return nil, nil
	}
	ctx := r.Context()
	se, err := s.store.SessionByToken(ctx, auth.HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A bearer token must not be replayed as a cookie and vice versa.
	if bearer != (se.Kind == store.SessionApp) {
		return nil, nil
	}
	u, err := s.store.GetUser(ctx, se.UserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && u.Disabled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.store.TouchSession(ctx, se, clientIP(r)); err != nil {
		s.log.Warn("touch session", "err", err)
	}
	return &authCtx{User: u, Session: se, Bearer: bearer, Settings: st,
		MustChangePassword: u.MustChangePassword, MustEnroll2FA: requires2FA(st, u) && !u.TOTPEnabled}, nil
}

func (s *Server) authed(flags int) func(handlerFunc) handlerFunc {
	return func(next handlerFunc) handlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			a, err := s.loadAuth(r)
			if err != nil {
				return err
			}
			if a == nil {
				return errUnauthorized
			}
			if a.restricted() && flags&allowRestricted == 0 {
				code, msg := "2fa_required", "set up two-factor authentication first"
				if a.MustChangePassword {
					code, msg = "password_change_required", "change your password first"
				}
				return &apiError{status: http.StatusForbidden, code: code, msg: msg}
			}
			if flags&needAdmin != 0 && !a.User.IsAdmin {
				return errForbidden
			}
			return next(w, r.WithContext(context.WithValue(r.Context(), keyAuth, a)))
		}
	}
}

// startSession creates a session and delivers the token: as a cookie for browsers, in the
// JSON body for the app.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User, app bool, deviceName string) (string, *store.Session, error) {
	st, err := s.store.GetSettings(r.Context())
	if err != nil {
		return "", nil, err
	}
	token, hash := auth.NewToken()
	kind := store.SessionWeb
	idle := time.Duration(st.WebSessionIdleMinutes) * time.Minute
	maxAge := time.Duration(st.WebSessionMaxHours) * time.Hour
	name := deviceName
	if app {
		kind = store.SessionApp
		idle = time.Duration(st.AppSessionIdleDays) * 24 * time.Hour
		maxAge = time.Duration(st.AppSessionMaxDays) * 24 * time.Hour
	} else {
		name = describeUA(r.UserAgent())
	}
	se, err := s.store.CreateSession(r.Context(), hash, u.ID, kind, name, clientIP(r), idle, maxAge)
	if err != nil {
		return "", nil, err
	}
	if !app {
		https := client(r).HTTPS
		http.SetCookie(w, &http.Cookie{
			Name: cookieName(https), Value: token, Path: "/", HttpOnly: true, Secure: https,
			SameSite: http.SameSiteStrictMode, Expires: se.ExpiresAt,
		})
		token = ""
	}
	return token, se, nil
}

func (s *Server) clearCookie(w http.ResponseWriter, r *http.Request) {
	https := client(r).HTTPS
	http.SetCookie(w, &http.Cookie{Name: cookieName(https), Value: "", Path: "/", HttpOnly: true, Secure: https,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// describeUA turns a User-Agent into a short device label for the sessions list.
func describeUA(ua string) string {
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	osName := ""
	switch {
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		osName = "iOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "Mac OS X"):
		osName = "macOS"
	case strings.Contains(ua, "Linux"):
		osName = "Linux"
	}
	if osName == "" {
		return browser
	}
	return browser + " on " + osName
}

func (s *Server) audit(r *http.Request, action, target, details string, success bool) {
	e := store.AuditEntry{IP: clientIP(r), Action: action, Target: target, Details: details, Success: success}
	if a := authOf(r); a != nil {
		e.UserID, e.Username = a.User.ID, a.User.Username
	}
	if err := s.store.Audit(context.Background(), e); err != nil {
		s.log.Error("audit", "err", err)
	}
}
