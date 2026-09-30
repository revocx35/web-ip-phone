package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// clientInfo is resolved once per request by the base middleware.
type clientInfo struct {
	IP     netip.Addr
	HTTPS  bool
	Origin string // scheme://host of this server as the client sees it
}

type ctxKey int

const (
	keyClient ctxKey = iota
	keyAuth
)

func (s *Server) isTrustedProxy(a netip.Addr) bool {
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// resolveClient determines the client address and scheme. X-Forwarded-For and
// X-Forwarded-Proto are only honoured from trusted proxies; the client address is the
// right-most XFF entry that is not itself a trusted proxy (entries further left can be
// forged by the client).
func (s *Server) resolveClient(r *http.Request) clientInfo {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	peer, _ := netip.ParseAddr(host)
	peer = peer.Unmap()
	ci := clientInfo{IP: peer, HTTPS: r.TLS != nil}
	if peer.IsValid() && s.isTrustedProxy(peer) {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			parts := strings.Split(strings.Join(xff, ","), ",")
			for i := len(parts) - 1; i >= 0; i-- {
				a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
				if err != nil {
					break
				}
				a = a.Unmap()
				ci.IP = a
				if !s.isTrustedProxy(a) {
					break
				}
			}
		}
		if xfp := r.Header.Values("X-Forwarded-Proto"); len(xfp) > 0 {
			vals := strings.Split(xfp[len(xfp)-1], ",")
			ci.HTTPS = strings.EqualFold(strings.TrimSpace(vals[len(vals)-1]), "https")
		}
	}
	scheme := "http"
	if ci.HTTPS {
		scheme = "https"
	}
	ci.Origin = scheme + "://" + strings.ToLower(r.Host)
	return ci
}

func client(r *http.Request) clientInfo {
	ci, _ := r.Context().Value(keyClient).(clientInfo)
	return ci
}

func clientIP(r *http.Request) string {
	ci := client(r)
	if !ci.IP.IsValid() {
		return ""
	}
	return ci.IP.String()
}

// originAllowed checks a browser-supplied Origin against this server's own origin and the
// configured public origins.
func (s *Server) originAllowed(r *http.Request, origin string) bool {
	origin = strings.ToLower(strings.TrimRight(origin, "/"))
	if origin == client(r).Origin {
		return true
	}
	for _, o := range s.cfg.PublicOrigins {
		if origin == o {
			return true
		}
	}
	return false
}

// ---- JSON --------------------------------------------------------------------------

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func errBad(format string, a ...any) error {
	return &apiError{status: http.StatusBadRequest, code: "invalid", msg: fmt.Sprintf(format, a...)}
}

var (
	errNotFound     = &apiError{status: http.StatusNotFound, code: "not_found", msg: "not found"}
	errForbidden    = &apiError{status: http.StatusForbidden, code: "forbidden", msg: "forbidden"}
	errUnauthorized = &apiError{status: http.StatusUnauthorized, code: "unauthorized", msg: "sign in required"}
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.status, map[string]string{"error": ae.msg, "code": ae.code})
		return
	}
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error", "code": "internal"})
}

const maxBody = 64 << 10

// readJSON decodes a JSON body strictly: size-limited, unknown fields rejected, single value.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return &apiError{status: http.StatusUnsupportedMediaType, code: "invalid", msg: "expected application/json"}
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &apiError{status: http.StatusRequestEntityTooLarge, code: "invalid", msg: "request too large"}
		}
		return errBad("invalid JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errBad("invalid JSON: trailing data")
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errNotFound
	}
	return id, nil
}

func queryInt(r *http.Request, name string, def, min, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
