package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
)

const (
	maxContacts       = 2000 // per user, and in the shared phone book
	maxContactNumbers = 10
)

type contactInput struct {
	Name    string                `json:"name"`
	Numbers []store.ContactNumber `json:"numbers"`
	// Shared puts the contact into the phone book of all users (admins only).
	Shared bool `json:"shared"`
	// Favorite marks the contact for the signed-in user; nil leaves it unchanged.
	Favorite *bool `json:"favorite"`
}

type contactView struct {
	*store.Contact
	Shared   bool `json:"shared"`
	Editable bool `json:"editable"`
}

// contactNumber strips the separators people write phone numbers with ("+49 (30) 123-45 67"),
// so stored numbers can be dialed as they are. SIP user names with letters ("john.doe")
// keep their dots and dashes.
func contactNumber(s string) string {
	letters := strings.IndexFunc(s, unicode.IsLetter) >= 0
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r), r == '(', r == ')', r == '/':
			return -1
		case (r == '-' || r == '.') && !letters:
			return -1
		}
		return r
	}, s)
}

func (in *contactInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validDisplayText(in.Name, 80) {
		return errBad("name: 1 to 80 characters")
	}
	if len(in.Numbers) == 0 || len(in.Numbers) > maxContactNumbers {
		return errBad("a contact needs 1 to %d numbers", maxContactNumbers)
	}
	for i := range in.Numbers {
		n := &in.Numbers[i]
		n.Label = strings.TrimSpace(n.Label)
		if !validDisplayText(n.Label, 32) {
			return errBad("number label: at most 32 characters")
		}
		n.Number = contactNumber(n.Number)
		if !sipua.ValidDialString(n.Number) {
			return errBad("invalid number %q: digits, letters and * # + . _ - only", n.Number)
		}
	}
	return nil
}

func (s *Server) contactView(r *http.Request, c *store.Contact) contactView {
	u := authOf(r).User
	return contactView{Contact: c, Shared: c.Shared(), Editable: c.OwnerID == u.ID || (c.Shared() && u.IsAdmin)}
}

func (s *Server) handleContacts(w http.ResponseWriter, r *http.Request) error {
	list, err := s.store.ListContacts(r.Context(), authOf(r).User.ID)
	if err != nil {
		return err
	}
	out := make([]contactView, len(list))
	for i, c := range list {
		out[i] = s.contactView(r, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": out})
	return nil
}

var errSharedContacts = &apiError{status: http.StatusForbidden, code: "forbidden", msg: "only administrators can change shared contacts"}

// contactOwner is the owner ID for a new or edited contact (0 = shared).
func contactOwner(r *http.Request, shared bool) (int64, error) {
	u := authOf(r).User
	if !shared {
		return u.ID, nil
	}
	if !u.IsAdmin {
		return 0, errSharedContacts
	}
	return 0, nil
}

func (s *Server) checkContactLimit(r *http.Request, owner int64) error {
	n, err := s.store.CountContacts(r.Context(), owner)
	if err != nil {
		return err
	}
	if n >= maxContacts {
		return errBad("the phone book is full (%d contacts)", maxContacts)
	}
	return nil
}

// saveContactDone finishes a create/update: favorite, audit, live update of the clients.
func (s *Server) saveContactDone(w http.ResponseWriter, r *http.Request, status int, id int64, fav *bool, action string, shared bool) error {
	ctx := r.Context()
	uid := authOf(r).User.ID
	if fav != nil {
		if err := s.store.SetFavorite(ctx, uid, id, *fav); err != nil {
			return err
		}
	}
	c, err := s.store.GetContact(ctx, uid, id)
	if err != nil {
		return err
	}
	if shared {
		s.audit(r, action, c.Name, "shared contact "+strconv.FormatInt(id, 10), true)
		s.svc.ContactsChanged(0)
	} else {
		s.svc.ContactsChanged(uid)
	}
	writeJSON(w, status, s.contactView(r, c))
	return nil
}

func (s *Server) handleCreateContact(w http.ResponseWriter, r *http.Request) error {
	var in contactInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.normalize(); err != nil {
		return err
	}
	owner, err := contactOwner(r, in.Shared)
	if err != nil {
		return err
	}
	if err := s.checkContactLimit(r, owner); err != nil {
		return err
	}
	id, err := s.store.CreateContact(r.Context(), &store.Contact{OwnerID: owner, Name: in.Name, Numbers: in.Numbers})
	if err != nil {
		return err
	}
	return s.saveContactDone(w, r, http.StatusCreated, id, in.Favorite, "contact.create", in.Shared)
}

// editableContact loads a contact the user may change: their own, or a shared one for admins.
func (s *Server) editableContact(r *http.Request) (*store.Contact, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	c, err := s.store.GetContact(r.Context(), authOf(r).User.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	if !s.contactView(r, c).Editable {
		return nil, errSharedContacts
	}
	return c, nil
}

func (s *Server) handleUpdateContact(w http.ResponseWriter, r *http.Request) error {
	var in contactInput
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	if err := in.normalize(); err != nil {
		return err
	}
	c, err := s.editableContact(r)
	if err != nil {
		return err
	}
	owner, err := contactOwner(r, in.Shared)
	if err != nil {
		return err
	}
	if owner != c.OwnerID {
		if err := s.checkContactLimit(r, owner); err != nil {
			return err
		}
	}
	wasShared := c.Shared()
	c.OwnerID, c.Name, c.Numbers = owner, in.Name, in.Numbers
	if err := s.store.UpdateContact(r.Context(), c); err != nil {
		return err
	}
	return s.saveContactDone(w, r, http.StatusOK, c.ID, in.Favorite, "contact.update", wasShared || in.Shared)
}

func (s *Server) handleDeleteContact(w http.ResponseWriter, r *http.Request) error {
	c, err := s.editableContact(r)
	if err != nil {
		return err
	}
	if err := s.store.DeleteContact(r.Context(), c.ID); err != nil {
		return err
	}
	if c.Shared() {
		s.audit(r, "contact.delete", c.Name, "shared contact "+strconv.FormatInt(c.ID, 10), true)
		s.svc.ContactsChanged(0)
	} else {
		s.svc.ContactsChanged(c.OwnerID)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleFavoriteContact(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Favorite bool `json:"favorite"`
	}
	if err := readJSON(w, r, &in); err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	uid := authOf(r).User.ID
	if err := s.store.SetFavorite(ctx, uid, id, in.Favorite); errors.Is(err, store.ErrNotFound) {
		return errNotFound
	} else if err != nil {
		return err
	}
	c, err := s.store.GetContact(ctx, uid, id)
	if err != nil {
		return err
	}
	s.svc.ContactsChanged(uid)
	writeJSON(w, http.StatusOK, s.contactView(r, c))
	return nil
}
