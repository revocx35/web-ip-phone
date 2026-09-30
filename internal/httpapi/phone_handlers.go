package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/revocx35/web-ip-phone/internal/phone"
	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
)

type phoneInput struct {
	PBXID       int64   `json:"pbxId"`
	Label       string  `json:"label"`
	SIPUser     string  `json:"sipUser"`
	AuthUser    string  `json:"authUser"`
	Password    *string `json:"password"`
	DisplayName string  `json:"displayName"`
	Register    bool    `json:"register"`
	// Verify checks the credentials with the PBX before saving.
	Verify bool `json:"verify"`
}

func (in *phoneInput) validate(create bool) error {
	switch {
	case !validDisplayText(in.Label, 64):
		return errBad("label: at most 64 characters")
	case !sipua.ValidUser(in.SIPUser):
		return errBad("extension / SIP user: letters, digits and + * . _ ~ - only")
	case in.AuthUser != "" && !sipua.ValidAuthUser(in.AuthUser):
		return errBad("invalid authentication username")
	case !sipua.ValidDisplayName(in.DisplayName):
		return errBad("caller ID name: at most 64 characters, no quotes or backslashes")
	case create && in.Password == nil:
		return errBad("password required")
	case in.Password != nil && !sipua.ValidPassword(*in.Password):
		return errBad("invalid SIP password")
	}
	return nil
}

var tokenEnc = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newContactToken is the unguessable user part of our Contact URI for a phone.
func newContactToken() string {
	b := make([]byte, 15)
	rand.Read(b)
	return "w" + tokenEnc.EncodeToString(b)
}

func (s *Server) sealPhone(p *store.Phone, password string) {
	p.Secret = s.box.Seal([]byte(password), p.SecretContext())
}

// verifyPhone checks credentials with the PBX (rate limited: it could otherwise be used to
// guess SIP passwords through this server).
func (s *Server) verifyPhone(r *http.Request, ph *store.Phone, password string) error {
	a := authOf(r)
	if !s.sipCheckLimiter.Allow(strconv.FormatInt(a.User.ID, 10)) {
		return &apiError{status: http.StatusTooManyRequests, code: "rate_limited", msg: "too many credential checks, wait a few minutes"}
	}
	pbx, err := s.store.GetPBX(r.Context(), ph.PBXID)
	if err != nil {
		return err
	}
	acct := &sipua.Account{PBX: phone.ToSIPPBX(pbx), User: ph.SIPUser, AuthUser: ph.AuthUser, Password: password, DisplayName: ph.DisplayName}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := s.sip.CheckCredentials(ctx, acct); err != nil {
		s.audit(r, "phone.verify", ph.SIPUser+"@"+pbx.Name, err.Error(), false)
		return &apiError{status: http.StatusUnprocessableEntity, code: "sip_check_failed", msg: err.Error()}
	}
	return nil
}

type pbxRef struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Mode         string `json:"mode"`
	CanAddPhones bool   `json:"canAddPhones"`
	Restricted   bool   `json:"restricted"` // dial rules apply
}

func (s *Server) handlePhones(w http.ResponseWriter, r *http.Request) error {
	a := authOf(r)
	ctx := r.Context()
	phones, err := s.store.UsablePhones(ctx, a.User.ID)
	if err != nil {
		return err
	}
	owned, err := s.store.ListOwnedPhones(ctx, a.User.ID)
	if err != nil {
		return err
	}
	access, err := s.store.UserAccess(ctx, a.User.ID)
	if err != nil {
		return err
	}
	pbxs, err := s.store.ListPBXs(ctx)
	if err != nil {
		return err
	}
	names := map[int64]*store.PBX{}
	for _, p := range pbxs {
		names[p.ID] = p
	}
	refs := []pbxRef{}
	for _, ac := range access {
		p := names[ac.PBXID]
		if p == nil || !p.Enabled {
			continue
		}
		refs = append(refs, pbxRef{ID: p.ID, Name: p.Name, Mode: ac.Mode, CanAddPhones: ac.Mode == store.AccessAny,
			Restricted: strings.TrimSpace(ac.DialRules) != ""})
	}
	type phoneView struct {
		*store.Phone
		PBXName string         `json:"pbxName"`
		Owned   bool           `json:"owned"`
		Usable  bool           `json:"usable"`
		Reg     sipua.RegState `json:"reg"`
	}
	out := []phoneView{}
	seen := map[int64]bool{}
	for _, p := range phones {
		seen[p.ID] = true
		pn := ""
		if x := names[p.PBXID]; x != nil {
			pn = x.Name
		}
		out = append(out, phoneView{Phone: p, PBXName: pn, Owned: p.OwnerID == a.User.ID, Usable: true, Reg: s.svc.RegState(p.ID)})
	}
	// Own phones the user can no longer use (access revoked) are listed so they can be deleted.
	for _, p := range owned {
		if seen[p.ID] {
			continue
		}
		pn := ""
		if x := names[p.PBXID]; x != nil {
			pn = x.Name
		}
		out = append(out, phoneView{Phone: p, PBXName: pn, Owned: true, Usable: false, Reg: sipua.RegState{Status: "off"}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"phones": out, "pbxs": refs})
	return nil
}

func (s *Server) handleCreatePhone(w http.ResponseWriter, r *http.Request) error {
	var in phoneInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(true); err != nil {
		return err
	}
	a := authOf(r)
	ctx := r.Context()
	acc, err := s.store.AccessFor(ctx, a.User.ID, in.PBXID)
	if err != nil || acc.Mode != store.AccessAny {
		return &apiError{status: http.StatusForbidden, code: "forbidden", msg: "you may not add phones on this PBX"}
	}
	pbx, err := s.store.GetPBX(ctx, in.PBXID)
	if err != nil || !pbx.Enabled {
		return &apiError{status: http.StatusForbidden, code: "forbidden", msg: "this PBX is not available"}
	}
	owned, err := s.store.ListOwnedPhones(ctx, a.User.ID)
	if err != nil {
		return err
	}
	if len(owned) >= 20 {
		return errBad("you can have at most 20 phones")
	}
	ph := &store.Phone{PBXID: in.PBXID, OwnerID: a.User.ID, Label: in.Label, SIPUser: in.SIPUser, AuthUser: in.AuthUser,
		DisplayName: in.DisplayName, Register: in.Register, ContactToken: newContactToken()}
	if in.Verify {
		if err := s.verifyPhone(r, ph, *in.Password); err != nil {
			return err
		}
	}
	s.sealPhone(ph, *in.Password)
	created, err := s.store.CreatePhone(ctx, ph)
	if err != nil {
		return err
	}
	s.audit(r, "phone.create", in.SIPUser+"@"+pbx.Name, "own phone "+strconv.FormatInt(created.ID, 10), true)
	s.svc.Revalidate(ctx)
	writeJSON(w, http.StatusCreated, created)
	return nil
}

func (s *Server) ownPhone(r *http.Request) (*store.Phone, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	ph, err := s.store.GetPhone(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && ph.OwnerID != authOf(r).User.ID) {
		return nil, errNotFound
	}
	return ph, err
}

func (s *Server) handleUpdatePhone(w http.ResponseWriter, r *http.Request) error {
	var in phoneInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(false); err != nil {
		return err
	}
	ph, err := s.ownPhone(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if acc, err := s.store.AccessFor(ctx, authOf(r).User.ID, ph.PBXID); err != nil || acc.Mode != store.AccessAny {
		return &apiError{status: http.StatusForbidden, code: "forbidden", msg: "you may no longer change phones on this PBX"}
	}
	return s.applyPhoneUpdate(w, r, ph, &in)
}

// applyPhoneUpdate saves edits to a phone (user-owned or admin-provisioned).
func (s *Server) applyPhoneUpdate(w http.ResponseWriter, r *http.Request, ph *store.Phone, in *phoneInput) error {
	ctx := r.Context()
	ph.Label, ph.SIPUser, ph.AuthUser, ph.DisplayName, ph.Register = in.Label, in.SIPUser, in.AuthUser, in.DisplayName, in.Register
	password := ""
	if in.Password != nil {
		password = *in.Password
	} else {
		pw, err := s.box.Open(ph.Secret, ph.SecretContext())
		if err != nil {
			return err
		}
		password = string(pw)
	}
	if in.Verify {
		if err := s.verifyPhone(r, ph, password); err != nil {
			return err
		}
	}
	if in.Password != nil {
		s.sealPhone(ph, password)
	} else {
		ph.Secret = nil
	}
	if err := s.store.UpdatePhone(ctx, ph); err != nil {
		return err
	}
	s.audit(r, "phone.update", ph.SIPUser, "phone "+strconv.FormatInt(ph.ID, 10), true)
	s.svc.Revalidate(ctx)
	updated, err := s.store.GetPhone(ctx, ph.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) handleDeletePhone(w http.ResponseWriter, r *http.Request) error {
	ph, err := s.ownPhone(r)
	if err != nil {
		return err
	}
	if err := s.store.DeletePhone(r.Context(), ph.ID); err != nil {
		return err
	}
	s.audit(r, "phone.delete", ph.SIPUser, "phone "+strconv.FormatInt(ph.ID, 10), true)
	s.svc.Revalidate(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleTestPhone(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ph, err := s.store.UsablePhone(r.Context(), authOf(r).User.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound
	}
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

func (s *Server) handleCalls(w http.ResponseWriter, r *http.Request) error {
	calls, err := s.store.UserCalls(r.Context(), authOf(r).User.ID, queryInt(r, "limit", 100, 1, 500))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, calls)
	return nil
}

func (s *Server) handleClearCalls(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.ClearUserCalls(r.Context(), authOf(r).User.ID); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
