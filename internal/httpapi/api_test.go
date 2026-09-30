package httpapi

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/config"
	"github.com/revocx35/web-ip-phone/internal/phone"
	"github.com/revocx35/web-ip-phone/internal/secretbox"
	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
)

type fakeEngine struct{}

func (fakeEngine) Dial(*sipua.Account, string, sipua.CallListener) (*sipua.Call, error) {
	return nil, errors.New("no SIP in unit tests")
}
func (fakeEngine) SetRegistration(*sipua.Account, bool) {}
func (fakeEngine) StopRegistration(int64)               {}
func (fakeEngine) Registration(int64) sipua.RegState    { return sipua.RegState{Status: "off"} }
func (fakeEngine) SetPBXs([]sipua.PBX)                  {}

type fakeSIP struct{}

func (fakeSIP) CheckCredentials(_ context.Context, a *sipua.Account) error {
	if a.Password != "right" {
		return errors.New("the PBX rejected the credentials")
	}
	return nil
}
func (fakeSIP) Ping(context.Context, *sipua.PBX) (time.Duration, int, error) {
	return time.Millisecond, 200, nil
}

type env struct {
	t     *testing.T
	s     *Server
	h     http.Handler
	now   time.Time
	store *store.Store
}

func newEnv(t *testing.T, proxies string) *env {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secretbox.New(make([]byte, 32))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := phone.New(st, box, log, "test")
	svc.SetEngine(fakeEngine{})
	tp, _ := config.ParseProxies(proxies)
	cfg := &config.Config{TrustedProxies: tp, PublicOrigins: []string{"https://phone.example.com"}}
	fast := auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1}
	s := New(Options{Config: cfg, Store: st, Box: box, Service: svc, SIP: fakeSIP{}, Logger: log, Version: "test",
		Static:     fstest.MapFS{"index.html": {Data: []byte("<!doctype html>ui")}, "assets/app.js": {Data: []byte("x")}},
		SetupToken: "ABCD-EFGH-JKLM-NPQR-STUV", PWParams: &fast})
	e := &env{t: t, s: s, h: s.Handler(), now: time.Unix(1_800_000_000, 0), store: st}
	clock := func() time.Time { return e.now }
	s.now = clock
	s.loginThrottle.SetClock(clock)
	s.mfaThrottle.SetClock(clock)
	s.setupThrottle.SetClock(clock)
	return e
}

type req struct {
	method, path string
	body         any
	cookie       string
	bearer       string
	headers      map[string]string
	remote       string
}

func (e *env) do(r req) (*httptest.ResponseRecorder, map[string]any) {
	var body io.Reader
	if r.body != nil {
		b, _ := json.Marshal(r.body)
		body = bytes.NewReader(b)
	}
	hr := httptest.NewRequest(r.method, "https://phone.local"+r.path, body)
	hr.RemoteAddr = "198.51.100.10:5555"
	if r.remote != "" {
		hr.RemoteAddr = r.remote
	}
	if r.body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.bearer != "" {
		hr.Header.Set("Authorization", "Bearer "+r.bearer)
	} else if r.method != "GET" {
		hr.Header.Set("X-Requested-With", "webphone")
	}
	if r.cookie != "" {
		hr.Header.Set("Cookie", "__Host-wip_session="+r.cookie)
	}
	for k, v := range r.headers {
		if v == "" {
			hr.Header.Del(k)
		} else {
			hr.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, hr)
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func cookieOf(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-wip_session" {
			return c.Value
		}
	}
	return ""
}

// setupAdmin creates the admin and returns its session cookie.
func (e *env) setupAdmin() string {
	w, m := e.do(req{method: "POST", path: "/api/v1/setup", body: map[string]string{"token": "abcd efgh jklm npqr stuv", "username": "admin", "password": "Adm1n-Secret!"}})
	if w.Code != 200 {
		e.t.Fatalf("setup: %d %v", w.Code, m)
	}
	c := cookieOf(w)
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		e.t.Fatalf("cookie attributes: %+v", cookie)
	}
	return c
}

func (e *env) login(user, pw string, app bool) (string, map[string]any) {
	body := map[string]string{"username": user, "password": pw}
	if app {
		body["client"] = "app"
	}
	w, m := e.do(req{method: "POST", path: "/api/v1/auth/login", body: body})
	if w.Code != 200 {
		return "", m
	}
	if app {
		tok, _ := m["token"].(string)
		return tok, m
	}
	return cookieOf(w), m
}

func TestSecurityHeadersAndSPA(t *testing.T) {
	e := newEnv(t, "")
	w, _ := e.do(req{method: "GET", path: "/admin/users"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ui") {
		t.Fatalf("spa fallback: %d", w.Code)
	}
	h := w.Header()
	for k, want := range map[string]string{"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin"} {
		if h.Get(k) != want {
			t.Errorf("%s = %q", k, h.Get(k))
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if !strings.HasPrefix(h.Get("Strict-Transport-Security"), "max-age=") {
		t.Error("no HSTS over TLS")
	}
	w, _ = e.do(req{method: "GET", path: "/assets/app.js"})
	if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Error("assets not cacheable")
	}
	w, _ = e.do(req{method: "GET", path: "/api/v1/nope"})
	if w.Code != 404 {
		t.Error("unknown API path")
	}
}

func TestSetupFlow(t *testing.T) {
	e := newEnv(t, "")
	w, _ := e.do(req{method: "POST", path: "/api/v1/setup", body: map[string]string{"token": "WRONG", "username": "admin", "password": "Adm1n-Secret!"}})
	if w.Code != 403 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	for i := 0; i < 5; i++ {
		e.do(req{method: "POST", path: "/api/v1/setup", body: map[string]string{"token": "WRONG"}})
	}
	w, _ = e.do(req{method: "POST", path: "/api/v1/setup", body: map[string]string{"token": "ABCD-EFGH-JKLM-NPQR-STUV", "username": "admin", "password": "Adm1n-Secret!"}})
	if w.Code != 429 {
		t.Fatalf("setup guessing not throttled: %d", w.Code)
	}
	e.now = e.now.Add(time.Hour + time.Minute)
	e.setupAdmin()
	w, _ = e.do(req{method: "POST", path: "/api/v1/setup", body: map[string]string{"token": "ABCD-EFGH-JKLM-NPQR-STUV", "username": "evil", "password": "Adm1n-Secret!"}})
	if w.Code != 409 {
		t.Fatalf("second setup: %d", w.Code)
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t, "")
	c := e.setupAdmin()
	body := map[string]string{"displayName": "x"}
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"no custom header", map[string]string{"X-Requested-With": ""}, 403},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"same-site but other origin", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, 403},
		{"own origin", map[string]string{"Origin": "https://phone.local", "Sec-Fetch-Site": "same-origin"}, 200},
		{"configured public origin", map[string]string{"Origin": "https://phone.example.com"}, 200},
	}
	for _, tc := range cases {
		w, _ := e.do(req{method: "PATCH", path: "/api/v1/me", body: body, cookie: c, headers: tc.headers})
		if w.Code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, w.Code, tc.want)
		}
	}
	// wrong content type is refused (a cross-site form post cannot send JSON)
	w, _ := e.do(req{method: "PATCH", path: "/api/v1/me", body: body, cookie: c, headers: map[string]string{"Content-Type": "text/plain"}})
	if w.Code != 415 {
		t.Errorf("text/plain body: %d", w.Code)
	}
}

func TestStrictJSON(t *testing.T) {
	e := newEnv(t, "")
	c := e.setupAdmin()
	w, _ := e.do(req{method: "PATCH", path: "/api/v1/me", body: map[string]any{"displayName": "x", "isAdmin": true}, cookie: c})
	if w.Code != 400 {
		t.Errorf("unknown field accepted: %d", w.Code)
	}
	w, _ = e.do(req{method: "PATCH", path: "/api/v1/me", body: map[string]string{"displayName": strings.Repeat("x", 70<<10)}, cookie: c})
	if w.Code != 413 {
		t.Errorf("huge body: %d", w.Code)
	}
}

func TestClientIPAndProxies(t *testing.T) {
	e := newEnv(t, "10.0.0.0/8")
	resolve := func(remote string, h map[string]string) clientInfo {
		r := httptest.NewRequest("GET", "http://phone.local/", nil)
		r.RemoteAddr = remote
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return e.s.resolveClient(r)
	}
	// untrusted peer: headers ignored
	ci := resolve("203.0.113.5:1000", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Forwarded-Proto": "https"})
	if ci.IP.String() != "203.0.113.5" || ci.HTTPS {
		t.Errorf("spoofed: %+v", ci)
	}
	// trusted proxy: right-most untrusted entry; a client-supplied left entry is ignored
	ci = resolve("10.0.0.2:1000", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7, 10.0.0.9", "X-Forwarded-Proto": "https"})
	if ci.IP.String() != "198.51.100.7" || !ci.HTTPS || ci.Origin != "https://phone.local" {
		t.Errorf("proxied: %+v", ci)
	}
	ci = resolve("10.0.0.2:1000", map[string]string{"X-Forwarded-For": "garbage"})
	if ci.IP.String() != "10.0.0.2" {
		t.Errorf("garbage XFF: %+v", ci)
	}
}

func TestLoginThrottleAndGenericErrors(t *testing.T) {
	e := newEnv(t, "")
	e.setupAdmin()
	_, m1 := e.login("admin", "wrong-1", false)
	_, m2 := e.login("nobody", "wrong-1", false)
	if m1["error"] != m2["error"] || m1["code"] != "bad_credentials" {
		t.Fatalf("errors reveal user existence: %v / %v", m1, m2)
	}
	for i := 0; i < 5; i++ {
		e.login("admin", "wrong", false)
	}
	if _, m := e.login("admin", "Adm1n-Secret!", false); m["code"] != "rate_limited" {
		t.Fatalf("not throttled: %v", m)
	}
	e.now = e.now.Add(20 * time.Minute)
	if c, m := e.login("admin", "Adm1n-Secret!", false); c == "" {
		t.Fatalf("login after the wait failed: %v", m)
	}
}

func TestTokenKinds(t *testing.T) {
	e := newEnv(t, "")
	web := e.setupAdmin()
	app, _ := e.login("admin", "Adm1n-Secret!", true)
	if app == "" {
		t.Fatal("no app token")
	}
	if w, _ := e.do(req{method: "GET", path: "/api/v1/me", bearer: app}); w.Code != 200 {
		t.Fatal("app token rejected")
	}
	if w, _ := e.do(req{method: "GET", path: "/api/v1/me", bearer: web}); w.Code != 401 {
		t.Fatal("web session accepted as bearer token")
	}
	if w, _ := e.do(req{method: "GET", path: "/api/v1/me", cookie: app}); w.Code != 401 {
		t.Fatal("app token accepted as cookie")
	}
	// logout ends the session
	e.do(req{method: "POST", path: "/api/v1/auth/logout", bearer: app, body: map[string]string{}})
	if w, _ := e.do(req{method: "GET", path: "/api/v1/me", bearer: app}); w.Code != 401 {
		t.Fatal("token alive after logout")
	}
}

func TestAdminBoundaryAndRestrictedSessions(t *testing.T) {
	e := newEnv(t, "")
	admin := e.setupAdmin()
	w, _ := e.do(req{method: "POST", path: "/api/v1/admin/users", cookie: admin, body: map[string]any{"username": "carol", "password": "Tempor4ry-Pass", "mustChangePassword": true}})
	if w.Code != 201 {
		t.Fatalf("create user: %d", w.Code)
	}
	carol, _ := e.login("carol", "Tempor4ry-Pass", false)
	if w, m := e.do(req{method: "GET", path: "/api/v1/phones", cookie: carol}); w.Code != 403 || m["code"] != "password_change_required" {
		t.Fatalf("restricted session reached phones: %d %v", w.Code, m)
	}
	if w, _ := e.do(req{method: "GET", path: "/api/v1/me", cookie: carol}); w.Code != 200 {
		t.Fatal("restricted session cannot read /me")
	}
	if w, _ := e.do(req{method: "POST", path: "/api/v1/me/password", cookie: carol, body: map[string]string{"current": "Tempor4ry-Pass", "new": "Her-0wn-Secret-99"}}); w.Code != 200 {
		t.Fatal("password change failed")
	}
	if w, _ := e.do(req{method: "GET", path: "/api/v1/phones", cookie: carol}); w.Code != 200 {
		t.Fatal("still restricted after changing the password")
	}
	for _, p := range []string{"/api/v1/admin/users", "/api/v1/admin/pbxs", "/api/v1/admin/audit", "/api/v1/admin/settings"} {
		if w, _ := e.do(req{method: "GET", path: p, cookie: carol}); w.Code != 403 {
			t.Errorf("non-admin reached %s: %d", p, w.Code)
		}
	}
	if w, _ := e.do(req{method: "GET", path: "/api/v1/admin/users"}); w.Code != 401 {
		t.Error("anonymous reached admin API")
	}
	// the admin cannot delete or disable themselves
	if w, _ := e.do(req{method: "DELETE", path: "/api/v1/admin/users/1", cookie: admin}); w.Code != 400 {
		t.Errorf("self delete: %d", w.Code)
	}
	// "any credentials" mode: SIP credentials are checked before saving
	e.do(req{method: "POST", path: "/api/v1/admin/pbxs", cookie: admin, body: map[string]any{"name": "P", "host": "10.0.0.1", "transport": "udp", "enabled": true}})
	e.do(req{method: "PUT", path: "/api/v1/admin/users/2/access", cookie: admin, body: []map[string]any{{"pbxId": 1, "mode": "any"}}})
	if w, _ := e.do(req{method: "POST", path: "/api/v1/phones", cookie: carol, body: map[string]any{"pbxId": 1, "sipUser": "100", "password": "wrong", "verify": true}}); w.Code != 422 {
		t.Errorf("unverified phone saved: %d", w.Code)
	}
	w, m := e.do(req{method: "POST", path: "/api/v1/phones", cookie: carol, body: map[string]any{"pbxId": 1, "sipUser": "100", "password": "right", "verify": true}})
	if w.Code != 201 {
		t.Fatalf("phone: %d %v", w.Code, m)
	}
	if strings.Contains(w.Body.String(), "right") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("SIP password leaked in the response")
	}
	// header injection through SIP fields is refused
	if w, _ := e.do(req{method: "POST", path: "/api/v1/phones", cookie: carol, body: map[string]any{"pbxId": 1, "sipUser": "100\r\nX: y", "password": "right"}}); w.Code != 400 {
		t.Errorf("CRLF in SIP user: %d", w.Code)
	}
	if w, _ := e.do(req{method: "POST", path: "/api/v1/phones", cookie: carol, body: map[string]any{"pbxId": 1, "sipUser": "101", "password": "right", "displayName": "a\"b"}}); w.Code != 400 {
		t.Errorf("quote in display name: %d", w.Code)
	}
}

func TestTwoFactorLogin(t *testing.T) {
	e := newEnv(t, "")
	c := e.setupAdmin()
	_, m := e.do(req{method: "POST", path: "/api/v1/me/totp/setup", cookie: c, body: map[string]string{}})
	secret, err := b32.DecodeString(m["secret"].(string))
	if err != nil || !strings.HasPrefix(m["qrPng"].(string), "data:image/png;base64,") {
		t.Fatalf("setup: %v %v", err, m)
	}
	if w, _ := e.do(req{method: "POST", path: "/api/v1/me/totp/enable", cookie: c, body: map[string]string{"code": "000000"}}); w.Code != 400 {
		t.Fatal("wrong enable code accepted")
	}
	w, m := e.do(req{method: "POST", path: "/api/v1/me/totp/enable", cookie: c, body: map[string]string{"code": auth.TOTPCode(secret, e.now)}})
	if w.Code != 200 {
		t.Fatalf("enable: %v", m)
	}
	codes := m["recoveryCodes"].([]any)
	if len(codes) != 10 {
		t.Fatal("recovery codes")
	}

	ticket := func() string {
		_, m := e.login("admin", "Adm1n-Secret!", false)
		if m["mfaRequired"] != true {
			t.Fatalf("no MFA step: %v", m)
		}
		return m["ticket"].(string)
	}
	mfa := func(tk, code string) int {
		w, _ := e.do(req{method: "POST", path: "/api/v1/auth/mfa", body: map[string]string{"ticket": tk, "code": code}})
		return w.Code
	}
	// the code used for enabling cannot be replayed
	if mfa(ticket(), auth.TOTPCode(secret, e.now)) != 401 {
		t.Fatal("replayed TOTP code accepted")
	}
	e.now = e.now.Add(30 * time.Second)
	tk := ticket()
	if mfa(tk, "123456") != 401 {
		t.Fatal("wrong code accepted")
	}
	if mfa(tk, auth.TOTPCode(secret, e.now)) != 200 {
		t.Fatal("valid code rejected")
	}
	if mfa(tk, auth.TOTPCode(secret, e.now)) != 401 {
		t.Fatal("ticket reused")
	}
	rc := codes[0].(string)
	if mfa(ticket(), strings.ToLower(rc)) != 200 {
		t.Fatal("recovery code rejected")
	}
	if mfa(ticket(), rc) != 401 {
		t.Fatal("recovery code reused")
	}
	// tickets expire
	tk = ticket()
	e.now = e.now.Add(6 * time.Minute)
	if mfa(tk, auth.TOTPCode(secret, e.now)) != 401 {
		t.Fatal("expired ticket accepted")
	}
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)
