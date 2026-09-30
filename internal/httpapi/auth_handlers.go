package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"rsc.io/qr"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/store"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$`)

func validDisplayText(s string, max int) bool {
	if utf8.RuneCountInString(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type meView struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"displayName"`
	IsAdmin            bool   `json:"isAdmin"`
	TOTPEnabled        bool   `json:"totpEnabled"`
	MustChangePassword bool   `json:"mustChangePassword"`
	MustEnroll2FA      bool   `json:"mustEnroll2fa"`
	TwoFARequired      bool   `json:"twoFaRequired"`
	RecoveryCodesLeft  int    `json:"recoveryCodesLeft"`
	PasswordMinLength  int    `json:"passwordMinLength"`
	SessionID          int64  `json:"sessionId"`
}

func (s *Server) meViewOf(ctx context.Context, u *store.User, st store.Settings, sessionID int64) meView {
	left := 0
	if u.TOTPEnabled {
		left, _ = s.store.CountRecoveryCodes(ctx, u.ID)
	}
	return meView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin, TOTPEnabled: u.TOTPEnabled,
		MustChangePassword: u.MustChangePassword, MustEnroll2FA: requires2FA(st, u) && !u.TOTPEnabled,
		TwoFARequired: requires2FA(st, u), RecoveryCodesLeft: left, PasswordMinLength: st.PasswordMinLength, SessionID: sessionID}
}

func tooMany(wait time.Duration) error {
	return &apiError{status: http.StatusTooManyRequests, code: "rate_limited",
		msg: fmt.Sprintf("too many attempts, try again in %d seconds", int(wait.Seconds())+1)}
}

// ---- setup -------------------------------------------------------------------------

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]bool{"required": s.SetupPending()})
	return nil
}

type setupReq struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
	Client      string `json:"client"`
	DeviceName  string `json:"deviceName"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) error {
	var req setupReq
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	ipKey := "setup:" + clientIP(r)
	if wait := s.setupThrottle.Wait(ipKey); wait > 0 {
		return tooMany(wait)
	}
	s.setupMu.Lock()
	expected := s.setupToken
	s.setupMu.Unlock()
	if expected == "" {
		return &apiError{status: http.StatusConflict, code: "setup_done", msg: "setup is already complete"}
	}
	a, b := sha256.Sum256([]byte(auth.NormalizeHumanCode(req.Token))), sha256.Sum256([]byte(auth.NormalizeHumanCode(expected)))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		s.setupThrottle.Fail(ipKey)
		s.audit(r, "setup", req.Username, "wrong setup token", false)
		return &apiError{status: http.StatusForbidden, code: "bad_token", msg: "wrong setup token (see the server log)"}
	}
	if !usernameRe.MatchString(req.Username) {
		return errBad("username: 3-32 letters, digits, dot, dash or underscore")
	}
	if !validDisplayText(req.DisplayName, 64) {
		return errBad("invalid display name")
	}
	st := store.DefaultSettings()
	if err := auth.CheckPasswordPolicy(req.Password, req.Username, st.PasswordMinLength); err != nil {
		return errBad("%v", err)
	}
	hash, err := auth.HashPassword(r.Context(), req.Password, s.pwParams)
	if err != nil {
		return err
	}
	u, err := s.store.CreateFirstAdmin(r.Context(), store.NewUser{Username: req.Username, DisplayName: req.DisplayName, PasswordHash: hash})
	if errors.Is(err, store.ErrConflict) {
		return &apiError{status: http.StatusConflict, code: "setup_done", msg: "setup is already complete"}
	}
	if err != nil {
		return err
	}
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
	s.log.Info("first admin created", "username", u.Username)
	token, se, err := s.startSession(w, r, u, req.Client == "app", req.DeviceName)
	if err != nil {
		return err
	}
	r = r.WithContext(context.WithValue(r.Context(), keyAuth, &authCtx{User: u, Session: se}))
	s.audit(r, "setup", u.Username, "first admin created", true)
	resp := map[string]any{"user": s.meViewOf(r.Context(), u, st, se.ID)}
	if token != "" {
		resp["token"] = token
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// ---- login -------------------------------------------------------------------------

type loginReq struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Client     string `json:"client"` // "web" (cookie) or "app" (token)
	DeviceName string `json:"deviceName"`
}

type mfaTicket struct {
	userID   int64
	app      bool
	device   string
	expires  time.Time
	attempts int
}

var errBadLogin = &apiError{status: http.StatusUnauthorized, code: "bad_credentials", msg: "invalid username or password"}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req loginReq
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	if req.Client != "" && req.Client != "web" && req.Client != "app" {
		return errBad("client must be web or app")
	}
	if !validDisplayText(req.DeviceName, 64) {
		return errBad("invalid device name")
	}
	ipKey, userKey := "ip:"+clientIP(r), "user:"+strings.ToLower(req.Username)
	if wait := s.loginThrottle.Wait(ipKey, userKey); wait > 0 {
		return tooMany(wait)
	}
	fail := func(details string) error {
		s.loginThrottle.Fail(ipKey, userKey)
		s.audit(r, "login", req.Username, details, false)
		return errBadLogin
	}
	if len(req.Username) > 64 || len(req.Password) > auth.MaxPasswordLen || req.Password == "" {
		return fail("malformed")
	}
	u, err := s.store.GetUserByUsername(r.Context(), req.Username)
	if errors.Is(err, store.ErrNotFound) {
		auth.BurnPasswordCheck(r.Context(), req.Password, s.pwParams)
		return fail("unknown user")
	}
	if err != nil {
		return err
	}
	ok, rehash, err := auth.VerifyPassword(r.Context(), req.Password, u.PasswordHash, s.pwParams)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("verify password", "user", u.Username, "err", err)
	}
	if !ok {
		return fail("wrong password")
	}
	if u.Disabled {
		return fail("account disabled")
	}
	s.loginThrottle.Reset(userKey)
	if rehash {
		if h, err := auth.HashPassword(r.Context(), req.Password, s.pwParams); err == nil {
			s.store.UpgradePasswordHash(r.Context(), u.ID, u.PasswordHash, h)
		}
	}
	app := req.Client == "app"
	if u.TOTPEnabled {
		ticket := randomHex(24)
		s.mfaMu.Lock()
		for k, t := range s.mfaTickets {
			if s.now().After(t.expires) {
				delete(s.mfaTickets, k)
			}
		}
		s.mfaTickets[ticket] = &mfaTicket{userID: u.ID, app: app, device: req.DeviceName, expires: s.now().Add(5 * time.Minute)}
		s.mfaMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"mfaRequired": true, "ticket": ticket})
		return nil
	}
	return s.finishLogin(w, r, u, app, req.DeviceName)
}

func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, u *store.User, app bool, device string) error {
	token, se, err := s.startSession(w, r, u, app, device)
	if err != nil {
		return err
	}
	st, _ := s.store.GetSettings(r.Context())
	r = r.WithContext(context.WithValue(r.Context(), keyAuth, &authCtx{User: u, Session: se}))
	kind := "web"
	if app {
		kind = "app"
	}
	s.audit(r, "login", u.Username, kind+" session "+strconv.FormatInt(se.ID, 10)+": "+se.Name, true)
	resp := map[string]any{"user": s.meViewOf(r.Context(), u, st, se.ID)}
	if token != "" {
		resp["token"] = token
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type mfaReq struct {
	Ticket string `json:"ticket"`
	Code   string `json:"code"`
}

func (s *Server) handleLoginMFA(w http.ResponseWriter, r *http.Request) error {
	var req mfaReq
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	s.mfaMu.Lock()
	t := s.mfaTickets[req.Ticket]
	if t != nil && s.now().After(t.expires) {
		delete(s.mfaTickets, req.Ticket)
		t = nil
	}
	s.mfaMu.Unlock()
	if t == nil {
		return &apiError{status: http.StatusUnauthorized, code: "mfa_expired", msg: "sign-in expired, start again"}
	}
	key := "mfa:" + strconv.FormatInt(t.userID, 10)
	if wait := s.mfaThrottle.Wait(key); wait > 0 {
		return tooMany(wait)
	}
	u, err := s.store.GetUser(r.Context(), t.userID)
	if err != nil || u.Disabled {
		return errBadLogin
	}
	ok, err := s.checkSecondFactor(r.Context(), u, req.Code)
	if err != nil {
		return err
	}
	if !ok {
		s.mfaThrottle.Fail(key)
		s.mfaMu.Lock()
		t.attempts++
		if t.attempts >= 5 {
			delete(s.mfaTickets, req.Ticket)
		}
		s.mfaMu.Unlock()
		s.audit(r, "login", u.Username, "wrong 2FA code", false)
		return &apiError{status: http.StatusUnauthorized, code: "bad_code", msg: "wrong code"}
	}
	s.mfaMu.Lock()
	delete(s.mfaTickets, req.Ticket)
	s.mfaMu.Unlock()
	s.mfaThrottle.Reset(key)
	return s.finishLogin(w, r, u, t.app, t.device)
}

func totpContext(userID int64) string { return "totp:" + strconv.FormatInt(userID, 10) }

// checkSecondFactor accepts a current TOTP code (each time step once) or an unused
// recovery code.
func (s *Server) checkSecondFactor(ctx context.Context, u *store.User, code string) (bool, error) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if !u.TOTPEnabled || code == "" || len(code) > 32 {
		return false, nil
	}
	if len(code) == 6 && strings.Trim(code, "0123456789") == "" {
		secret, err := s.box.Open(u.TOTPSecret, totpContext(u.ID))
		if err != nil {
			return false, err
		}
		step, ok := auth.CheckTOTP(secret, code, s.now())
		if !ok {
			return false, nil
		}
		return s.store.UseTOTPStep(ctx, u.ID, step)
	}
	return s.store.UseRecoveryCode(ctx, u.ID, auth.NormalizeHumanCode(code))
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	a := authOf(r)
	s.store.DeleteSession(r.Context(), a.User.ID, a.Session.ID)
	s.svc.DisconnectSession(a.Session.ID, "signed out")
	s.clearCookie(w, r)
	s.audit(r, "logout", a.User.Username, "", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

// ---- me ----------------------------------------------------------------------------

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) error {
	a := authOf(r)
	writeJSON(w, http.StatusOK, s.meViewOf(r.Context(), a.User, a.Settings, a.Session.ID))
	return nil
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		DisplayName string `json:"displayName"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	if !validDisplayText(req.DisplayName, 64) {
		return errBad("invalid display name")
	}
	a := authOf(r)
	if err := s.store.UpdateUser(r.Context(), a.User.ID, store.UserUpdate{DisplayName: &req.DisplayName}); err != nil {
		return err
	}
	a.User.DisplayName = req.DisplayName
	writeJSON(w, http.StatusOK, s.meViewOf(r.Context(), a.User, a.Settings, a.Session.ID))
	return nil
}

// verifyCurrentPassword re-authenticates a signed-in user for sensitive changes.
func (s *Server) verifyCurrentPassword(r *http.Request, password string) error {
	a := authOf(r)
	key := "reauth:" + strconv.FormatInt(a.User.ID, 10)
	if wait := s.loginThrottle.Wait(key); wait > 0 {
		return tooMany(wait)
	}
	if len(password) > auth.MaxPasswordLen {
		return errBad("password too long")
	}
	ok, _, err := auth.VerifyPassword(r.Context(), password, a.User.PasswordHash, s.pwParams)
	if err != nil {
		return err
	}
	if !ok {
		s.loginThrottle.Fail(key)
		return &apiError{status: http.StatusForbidden, code: "bad_password", msg: "current password is wrong"}
	}
	s.loginThrottle.Reset(key)
	return nil
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	a := authOf(r)
	if err := s.verifyCurrentPassword(r, req.Current); err != nil {
		s.audit(r, "password.change", a.User.Username, "wrong current password", false)
		return err
	}
	if req.New == req.Current {
		return errBad("the new password must differ from the current one")
	}
	if err := auth.CheckPasswordPolicy(req.New, a.User.Username, a.Settings.PasswordMinLength); err != nil {
		return errBad("%v", err)
	}
	hash, err := auth.HashPassword(r.Context(), req.New, s.pwParams)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(r.Context(), a.User.ID, hash, false, a.Session.ID); err != nil {
		return err
	}
	// Other sessions were deleted with the password change; close their connections.
	s.svc.DisconnectOtherSessions(a.User.ID, a.Session.ID, "password changed")
	s.audit(r, "password.change", a.User.Username, "other sessions signed out", true)
	u, _ := s.store.GetUser(r.Context(), a.User.ID)
	writeJSON(w, http.StatusOK, s.meViewOf(r.Context(), u, a.Settings, a.Session.ID))
	return nil
}

// ---- TOTP --------------------------------------------------------------------------

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) error {
	a := authOf(r)
	if a.User.TOTPEnabled {
		return &apiError{status: http.StatusConflict, code: "totp_enabled", msg: "two-factor authentication is already on"}
	}
	secret := auth.NewTOTPSecret()
	if err := s.store.SetTOTP(r.Context(), a.User.ID, s.box.Seal(secret, totpContext(a.User.ID)), false); err != nil {
		return err
	}
	uri := auth.TOTPURI(secret, "Web IP Phone", a.User.Username)
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return err
	}
	code.Scale = 6
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": auth.TOTPSecretString(secret),
		"uri":    uri,
		"qrPng":  "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()),
	})
	return nil
}

func newRecoveryCodes() (codes []string, hashes [][]byte) {
	for i := 0; i < 10; i++ {
		c := auth.NewHumanCode(3)
		h := sha256.Sum256([]byte(auth.NormalizeHumanCode(c)))
		codes = append(codes, c)
		hashes = append(hashes, h[:])
	}
	return codes, hashes
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Code string `json:"code"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	a := authOf(r)
	if a.User.TOTPEnabled {
		return &apiError{status: http.StatusConflict, code: "totp_enabled", msg: "two-factor authentication is already on"}
	}
	if a.User.TOTPSecret == nil {
		return errBad("start the setup first")
	}
	key := "totp-enable:" + strconv.FormatInt(a.User.ID, 10)
	if wait := s.mfaThrottle.Wait(key); wait > 0 {
		return tooMany(wait)
	}
	secret, err := s.box.Open(a.User.TOTPSecret, totpContext(a.User.ID))
	if err != nil {
		return err
	}
	step, ok := auth.CheckTOTP(secret, strings.TrimSpace(req.Code), s.now())
	if !ok {
		s.mfaThrottle.Fail(key)
		return &apiError{status: http.StatusBadRequest, code: "bad_code", msg: "wrong code, check the time on your phone"}
	}
	codes, hashes := newRecoveryCodes()
	if err := s.store.EnableTOTP(r.Context(), a.User.ID, step, hashes); err != nil {
		return err
	}
	s.audit(r, "2fa.enable", a.User.Username, "", true)
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
	return nil
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	a := authOf(r)
	if requires2FA(a.Settings, a.User) {
		return &apiError{status: http.StatusForbidden, code: "2fa_required", msg: "two-factor authentication is required by policy"}
	}
	if err := s.verifyCurrentPassword(r, req.Password); err != nil {
		return err
	}
	if err := s.store.DisableTOTP(r.Context(), a.User.ID); err != nil {
		return err
	}
	s.audit(r, "2fa.disable", a.User.Username, "", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleTOTPRecovery(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	a := authOf(r)
	if !a.User.TOTPEnabled {
		return errBad("two-factor authentication is off")
	}
	if err := s.verifyCurrentPassword(r, req.Password); err != nil {
		return err
	}
	codes, hashes := newRecoveryCodes()
	if err := s.store.ReplaceRecoveryCodes(r.Context(), a.User.ID, hashes); err != nil {
		return err
	}
	s.audit(r, "2fa.recovery_codes", a.User.Username, "regenerated", true)
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
	return nil
}

// ---- sessions ----------------------------------------------------------------------

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) error {
	a := authOf(r)
	list, err := s.store.ListSessions(r.Context(), a.User.ID)
	if err != nil {
		return err
	}
	type view struct {
		*store.Session
		Current bool `json:"current"`
	}
	out := make([]view, 0, len(list))
	for _, se := range list {
		out = append(out, view{Session: se, Current: se.ID == a.Session.ID})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a := authOf(r)
	if err := s.store.DeleteSession(r.Context(), a.User.ID, id); errors.Is(err, store.ErrNotFound) {
		return errNotFound
	} else if err != nil {
		return err
	}
	s.svc.DisconnectSession(id, "signed out from another device")
	if id == a.Session.ID {
		s.clearCookie(w, r)
	}
	s.audit(r, "session.revoke", a.User.Username, "session "+strconv.FormatInt(id, 10), true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
