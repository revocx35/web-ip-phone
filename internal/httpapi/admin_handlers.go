package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/dialrule"
	"github.com/revocx35/web-ip-phone/internal/phone"
	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
)

// ---- users -------------------------------------------------------------------------

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		return err
	}
	online := s.svc.ConnectedClients()
	type view struct {
		*store.User
		Connections int `json:"connections"`
	}
	out := make([]view, 0, len(users))
	for _, u := range users {
		out = append(out, view{User: u, Connections: online[u.ID]})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Username           string `json:"username"`
		DisplayName        string `json:"displayName"`
		Password           string `json:"password"`
		IsAdmin            bool   `json:"isAdmin"`
		MustChangePassword bool   `json:"mustChangePassword"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	if !usernameRe.MatchString(req.Username) {
		return errBad("username: 3-32 letters, digits, dot, dash or underscore")
	}
	if !validDisplayText(req.DisplayName, 64) {
		return errBad("invalid display name")
	}
	if err := auth.CheckPasswordPolicy(req.Password, req.Username, authOf(r).Settings.PasswordMinLength); err != nil {
		return errBad("%v", err)
	}
	hash, err := auth.HashPassword(r.Context(), req.Password, s.pwParams)
	if err != nil {
		return err
	}
	u, err := s.store.CreateUser(r.Context(), store.NewUser{Username: req.Username, DisplayName: req.DisplayName, PasswordHash: hash,
		IsAdmin: req.IsAdmin, MustChangePassword: req.MustChangePassword})
	if errors.Is(err, store.ErrConflict) {
		return &apiError{status: http.StatusConflict, code: "exists", msg: "a user with this name already exists"}
	}
	if err != nil {
		return err
	}
	s.audit(r, "user.create", u.Username, fmt.Sprintf("admin=%v", u.IsAdmin), true)
	writeJSON(w, http.StatusCreated, u)
	return nil
}

func (s *Server) targetUser(r *http.Request) (*store.User, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	u, err := s.store.GetUser(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound
	}
	return u, err
}

func storeErr(err error) error {
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		return &apiError{status: http.StatusConflict, code: "last_admin", msg: err.Error()}
	case errors.Is(err, store.ErrNotFound):
		return errNotFound
	case errors.Is(err, store.ErrConflict):
		return &apiError{status: http.StatusConflict, code: "exists", msg: "the name is already used"}
	}
	return err
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		DisplayName *string `json:"displayName"`
		IsAdmin     *bool   `json:"isAdmin"`
		Disabled    *bool   `json:"disabled"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	if req.DisplayName != nil && !validDisplayText(*req.DisplayName, 64) {
		return errBad("invalid display name")
	}
	if req.Disabled != nil && *req.Disabled && u.ID == authOf(r).User.ID {
		return errBad("you cannot disable your own account")
	}
	if err := s.store.UpdateUser(r.Context(), u.ID, store.UserUpdate{DisplayName: req.DisplayName, IsAdmin: req.IsAdmin, Disabled: req.Disabled}); err != nil {
		return storeErr(err)
	}
	var changes []string
	if req.IsAdmin != nil {
		changes = append(changes, fmt.Sprintf("admin=%v", *req.IsAdmin))
	}
	if req.Disabled != nil {
		changes = append(changes, fmt.Sprintf("disabled=%v", *req.Disabled))
		if *req.Disabled {
			s.svc.DisconnectUser(u.ID, "account disabled")
		}
	}
	if req.DisplayName != nil {
		changes = append(changes, "display name")
	}
	s.audit(r, "user.update", u.Username, strings.Join(changes, ", "), true)
	nu, err := s.store.GetUser(r.Context(), u.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, nu)
	return nil
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) error {
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	if u.ID == authOf(r).User.ID {
		return errBad("you cannot delete your own account")
	}
	if err := s.store.DeleteUser(r.Context(), u.ID); err != nil {
		return storeErr(err)
	}
	s.svc.DisconnectUser(u.ID, "account deleted")
	s.svc.Revalidate(r.Context())
	s.audit(r, "user.delete", u.Username, "", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleAdminSetPassword(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Password   string `json:"password"`
		MustChange bool   `json:"mustChange"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	if err := auth.CheckPasswordPolicy(req.Password, u.Username, authOf(r).Settings.PasswordMinLength); err != nil {
		return errBad("%v", err)
	}
	hash, err := auth.HashPassword(r.Context(), req.Password, s.pwParams)
	if err != nil {
		return err
	}
	keep := int64(0)
	if u.ID == authOf(r).User.ID {
		keep = authOf(r).Session.ID
	}
	if err := s.store.SetPassword(r.Context(), u.ID, hash, req.MustChange, keep); err != nil {
		return storeErr(err)
	}
	s.svc.DisconnectOtherSessions(u.ID, keep, "password reset by an admin")
	s.audit(r, "user.password_reset", u.Username, fmt.Sprintf("must change=%v", req.MustChange), true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleAdminReset2FA(w http.ResponseWriter, r *http.Request) error {
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	if err := s.store.DisableTOTP(r.Context(), u.ID); err != nil {
		return err
	}
	s.audit(r, "user.2fa_reset", u.Username, "", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleAdminRevokeSessions(w http.ResponseWriter, r *http.Request) error {
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	if err := s.store.DeleteUserSessions(r.Context(), u.ID); err != nil {
		return err
	}
	s.svc.DisconnectUser(u.ID, "signed out by an admin")
	s.audit(r, "user.sessions_revoked", u.Username, "", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

// ---- access ------------------------------------------------------------------------

func (s *Server) handleAdminGetAccess(w http.ResponseWriter, r *http.Request) error {
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	access, err := s.store.UserAccess(r.Context(), u.ID)
	if err != nil {
		return err
	}
	if access == nil {
		access = []*store.Access{}
	}
	writeJSON(w, http.StatusOK, access)
	return nil
}

func (s *Server) handleAdminSetAccess(w http.ResponseWriter, r *http.Request) error {
	var req []*store.Access
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	if len(req) > 100 {
		return errBad("too many entries")
	}
	seen := map[int64]bool{}
	var desc []string
	for _, a := range req {
		if a == nil || a.PBXID <= 0 || seen[a.PBXID] {
			return errBad("each PBX may appear once")
		}
		seen[a.PBXID] = true
		if a.Mode != store.AccessAny && a.Mode != store.AccessSelected {
			return errBad("mode must be any or selected")
		}
		if len(a.DialRules) > 4000 {
			return errBad("dial rules too long")
		}
		if _, err := dialrule.Parse(a.DialRules); err != nil {
			return errBad("dial rules: %v", err)
		}
		if len(a.PhoneIDs) > 200 {
			return errBad("too many phones")
		}
		pbx, err := s.store.GetPBX(r.Context(), a.PBXID)
		if err != nil {
			return errBad("unknown PBX %d", a.PBXID)
		}
		desc = append(desc, fmt.Sprintf("%s:%s(%d ext)", pbx.Name, a.Mode, len(a.PhoneIDs)))
	}
	if err := s.store.SetUserAccess(r.Context(), u.ID, req); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errBad("a selected extension does not belong to its PBX")
		}
		return storeErr(err)
	}
	s.svc.Revalidate(r.Context())
	s.audit(r, "user.access", u.Username, strings.Join(desc, ", "), true)
	access, err := s.store.UserAccess(r.Context(), u.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, access)
	return nil
}

// ---- PBXs --------------------------------------------------------------------------

type pbxInput struct {
	Name           string   `json:"name"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Transport      string   `json:"transport"`
	Domain         string   `json:"domain"`
	TLSVerify      bool     `json:"tlsVerify"`
	Codecs         []string `json:"codecs"`
	DTMFMode       string   `json:"dtmfMode"`
	RegisterExpiry int      `json:"registerExpiry"`
	Enabled        bool     `json:"enabled"`
	// GrantMe gives the creating admin "any credentials" access (create only).
	GrantMe bool `json:"grantMe"`
}

func (in *pbxInput) toPBX() (*store.PBX, error) {
	in.Name, in.Host, in.Domain = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.Domain)
	if in.Name == "" || !validDisplayText(in.Name, 64) {
		return nil, errBad("name: 1-64 characters")
	}
	if !sipua.ValidHost(in.Host) {
		return nil, errBad("host: a host name or IP address")
	}
	if in.Domain != "" && !sipua.ValidHost(in.Domain) {
		return nil, errBad("SIP domain: a host name or IP address, or empty")
	}
	switch in.Transport {
	case "udp", "tcp", "tls":
	default:
		return nil, errBad("transport: udp, tcp or tls")
	}
	if in.Port == 0 {
		in.Port = 5060
		if in.Transport == "tls" {
			in.Port = 5061
		}
	}
	if in.Port < 1 || in.Port > 65535 {
		return nil, errBad("port: 1-65535")
	}
	if len(in.Codecs) == 0 {
		in.Codecs = []string{"PCMU", "PCMA"}
	}
	var codecs []string
	for _, c := range in.Codecs {
		c = strings.ToUpper(c)
		if (c != "PCMU" && c != "PCMA") || slices.Contains(codecs, c) {
			return nil, errBad("codecs: PCMU and/or PCMA")
		}
		codecs = append(codecs, c)
	}
	if in.DTMFMode == "" {
		in.DTMFMode = "rfc4733"
	}
	if in.DTMFMode != "rfc4733" && in.DTMFMode != "info" {
		return nil, errBad("DTMF mode: rfc4733 or info")
	}
	if in.RegisterExpiry == 0 {
		in.RegisterExpiry = 300
	}
	if in.RegisterExpiry < 60 || in.RegisterExpiry > 3600 {
		return nil, errBad("registration interval: 60-3600 seconds")
	}
	return &store.PBX{Name: in.Name, Host: in.Host, Port: in.Port, Transport: in.Transport, Domain: in.Domain, TLSVerify: in.TLSVerify,
		Codecs: codecs, DTMFMode: in.DTMFMode, RegisterExpiry: in.RegisterExpiry, Enabled: in.Enabled}, nil
}

func (s *Server) handleAdminPBXs(w http.ResponseWriter, r *http.Request) error {
	pbxs, err := s.store.ListPBXs(r.Context())
	if err != nil {
		return err
	}
	if pbxs == nil {
		pbxs = []*store.PBX{}
	}
	writeJSON(w, http.StatusOK, pbxs)
	return nil
}

func (s *Server) handleAdminCreatePBX(w http.ResponseWriter, r *http.Request) error {
	var in pbxInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	p, err := in.toPBX()
	if err != nil {
		return err
	}
	created, err := s.store.CreatePBX(r.Context(), p)
	if err != nil {
		return storeErr(err)
	}
	if in.GrantMe {
		me := authOf(r).User.ID
		access, err := s.store.UserAccess(r.Context(), me)
		if err == nil {
			access = append(access, &store.Access{PBXID: created.ID, Mode: store.AccessAny})
			if err := s.store.SetUserAccess(r.Context(), me, access); err != nil {
				s.log.Error("grant access", "err", err)
			}
		}
	}
	s.svc.Revalidate(r.Context())
	s.audit(r, "pbx.create", created.Name, fmt.Sprintf("%s:%d/%s", created.Host, created.Port, created.Transport), true)
	writeJSON(w, http.StatusCreated, created)
	return nil
}

func (s *Server) handleAdminUpdatePBX(w http.ResponseWriter, r *http.Request) error {
	var in pbxInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := in.toPBX()
	if err != nil {
		return err
	}
	p.ID = id
	if err := s.store.UpdatePBX(r.Context(), p); err != nil {
		return storeErr(err)
	}
	s.svc.Revalidate(r.Context())
	s.audit(r, "pbx.update", p.Name, fmt.Sprintf("%s:%d/%s enabled=%v", p.Host, p.Port, p.Transport, p.Enabled), true)
	updated, err := s.store.GetPBX(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) handleAdminDeletePBX(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.store.GetPBX(r.Context(), id)
	if err != nil {
		return storeErr(err)
	}
	if err := s.store.DeletePBX(r.Context(), id); err != nil {
		return storeErr(err)
	}
	s.svc.Revalidate(r.Context())
	s.audit(r, "pbx.delete", p.Name, "", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleAdminPingPBX(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := s.store.GetPBX(r.Context(), id)
	if err != nil {
		return storeErr(err)
	}
	sp := phone.ToSIPPBX(p)
	rtt, code, err := s.sip.Ping(r.Context(), &sp)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rttMs": float64(rtt.Microseconds()) / 1000, "status": code})
	return nil
}

// ---- phones (extensions) -----------------------------------------------------------

type adminPhoneView struct {
	*store.Phone
	PBXName     string         `json:"pbxName"`
	OwnerName   string         `json:"ownerName"`
	Reg         sipua.RegState `json:"reg"`
	OnlineCount int            `json:"onlineCount"`
}

func (s *Server) handleAdminPhones(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	phones, err := s.store.ListAllPhones(ctx)
	if err != nil {
		return err
	}
	pbxs, _ := s.store.ListPBXs(ctx)
	users, _ := s.store.ListUsers(ctx)
	pn, un := map[int64]string{}, map[int64]string{}
	for _, p := range pbxs {
		pn[p.ID] = p.Name
	}
	for _, u := range users {
		un[u.ID] = u.Username
	}
	online := s.svc.OnlinePhones()
	out := make([]adminPhoneView, 0, len(phones))
	for _, p := range phones {
		out = append(out, adminPhoneView{Phone: p, PBXName: pn[p.PBXID], OwnerName: un[p.OwnerID], Reg: s.svc.RegState(p.ID), OnlineCount: online[p.ID]})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleAdminCreatePhone(w http.ResponseWriter, r *http.Request) error {
	var in phoneInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	pbxID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := in.validate(true); err != nil {
		return err
	}
	pbx, err := s.store.GetPBX(r.Context(), pbxID)
	if err != nil {
		return storeErr(err)
	}
	ph := &store.Phone{PBXID: pbxID, Label: in.Label, SIPUser: in.SIPUser, AuthUser: in.AuthUser, DisplayName: in.DisplayName,
		Register: in.Register, ContactToken: newContactToken()}
	if in.Verify {
		if err := s.verifyPhone(r, ph, *in.Password); err != nil {
			return err
		}
	}
	s.sealPhone(ph, *in.Password)
	created, err := s.store.CreatePhone(r.Context(), ph)
	if err != nil {
		return storeErr(err)
	}
	s.audit(r, "extension.create", in.SIPUser+"@"+pbx.Name, "", true)
	writeJSON(w, http.StatusCreated, created)
	return nil
}

func (s *Server) adminPhone(r *http.Request) (*store.Phone, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	ph, err := s.store.GetPhone(r.Context(), id)
	return ph, storeErr(err)
}

func (s *Server) handleAdminUpdatePhone(w http.ResponseWriter, r *http.Request) error {
	var in phoneInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(false); err != nil {
		return err
	}
	ph, err := s.adminPhone(r)
	if err != nil {
		return err
	}
	if ph.OwnerID != 0 {
		return errBad("this phone belongs to a user; only the user can edit it")
	}
	return s.applyPhoneUpdate(w, r, ph, &in)
}

func (s *Server) handleAdminDeletePhone(w http.ResponseWriter, r *http.Request) error {
	ph, err := s.adminPhone(r)
	if err != nil {
		return err
	}
	if err := s.store.DeletePhone(r.Context(), ph.ID); err != nil {
		return storeErr(err)
	}
	s.svc.Revalidate(r.Context())
	s.audit(r, "extension.delete", ph.SIPUser, "phone "+strconv.FormatInt(ph.ID, 10), true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleAdminTestPhone(w http.ResponseWriter, r *http.Request) error {
	ph, err := s.adminPhone(r)
	if err != nil {
		return err
	}
	pw, err := s.box.Open(ph.Secret, ph.SecretContext())
	if err != nil {
		return err
	}
	if err := s.verifyPhone(r, ph, string(pw)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

// ---- status, logs, settings --------------------------------------------------------

func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         s.version,
		"uptimeSeconds":   int(time.Since(s.started).Seconds()),
		"activeCalls":     s.svc.ActiveCalls(),
		"connections":     s.svc.ConnectedClients(),
		"certFingerprint": s.CertFingerprint,
		"sipPort":         s.cfg.SIPPort,
		"rtpPorts":        fmt.Sprintf("%d-%d", s.cfg.RTPPortMin, s.cfg.RTPPortMax),
	})
	return nil
}

func (s *Server) handleAdminHangup(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if len(id) != 24 || !s.svc.HangupCall(id) {
		return errNotFound
	}
	s.audit(r, "call.hangup", id, "by admin", true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleAdminCalls(w http.ResponseWriter, r *http.Request) error {
	calls, err := s.store.AllCalls(r.Context(), queryInt(r, "limit", 200, 1, 1000))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, calls)
	return nil
}

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) error {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	entries, err := s.store.ListAudit(r.Context(), before, queryInt(r, "limit", 100, 1, 500))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, entries)
	return nil
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.store.GetSettings(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}

func (s *Server) handleAdminPutSettings(w http.ResponseWriter, r *http.Request) error {
	var st store.Settings
	if err := readJSON(w, r, &st); err != nil {
		return err
	}
	if err := st.Validate(); err != nil {
		return errBad("%v", err)
	}
	if err := s.store.SaveSettings(r.Context(), st); err != nil {
		return err
	}
	s.audit(r, "settings.update", "", fmt.Sprintf("2fa=%s pwmin=%d calls/user=%d", st.Require2FA, st.PasswordMinLength, st.MaxCallsPerUser), true)
	writeJSON(w, http.StatusOK, st)
	return nil
}
