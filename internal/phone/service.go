package phone

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/revocx35/web-ip-phone/internal/dialrule"
	"github.com/revocx35/web-ip-phone/internal/secretbox"
	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
)

const (
	// unregisterDelay keeps a phone registered briefly after its last client left, so a
	// page reload or network switch does not bounce the registration.
	unregisterDelay = 45 * time.Second
	// detachGrace keeps a call up while its client reconnects (e.g. Wi-Fi -> mobile data).
	detachGrace = 30 * time.Second
)

// Engine is the part of sipua.Engine the service uses (an interface for tests).
type Engine interface {
	Dial(acct *sipua.Account, target string, l sipua.CallListener) (*sipua.Call, error)
	SetRegistration(acct *sipua.Account, on bool)
	StopRegistration(phoneID int64)
	Registration(phoneID int64) sipua.RegState
	SetPBXs(pbxs []sipua.PBX)
}

type Service struct {
	store   *store.Store
	box     *secretbox.Box
	log     *slog.Logger
	engine  Engine
	version string
	// MaxCalls caps simultaneous calls on the server.
	MaxCalls int

	mu          sync.Mutex
	clients     map[*Client]bool
	online      map[int64]map[*Client]bool // phone ID -> clients receiving its calls
	unregTimers map[int64]*time.Timer
	calls       map[string]*callEntry // by call ID
	regs        map[int64]sipua.RegState
}

// Client is one connected browser tab or app.
type Client struct {
	svc       *Service
	id        string
	UserID    int64
	Username  string
	SessionID int64
	out       Outbound
	userView  any

	// guarded by svc.mu
	phones map[int64]bool
	call   *callEntry // call whose audio this client carries
	closed bool
}

type callEntry struct {
	call    *sipua.Call
	phone   *store.Phone
	pbxName string
	userID  int64 // owner: caller or answerer (0 while an incoming call rings)
	client  *Client
	offered map[*Client]bool
	record  *store.CallRecord
	detach  *time.Timer
}

func New(st *store.Store, box *secretbox.Box, log *slog.Logger, version string) *Service {
	return &Service{
		store: st, box: box, log: log.With("component", "phone"), version: version, MaxCalls: 20,
		clients: map[*Client]bool{}, online: map[int64]map[*Client]bool{}, unregTimers: map[int64]*time.Timer{},
		calls: map[string]*callEntry{}, regs: map[int64]sipua.RegState{},
	}
}

// SetEngine attaches the SIP engine (created after the service, which is its Handler).
func (s *Service) SetEngine(e Engine) { s.engine = e }

func newClientID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ---- conversions -------------------------------------------------------------------

func ToSIPPBX(p *store.PBX) sipua.PBX {
	return sipua.PBX{ID: p.ID, Name: p.Name, Host: p.Host, Port: p.Port, Transport: p.Transport, Domain: p.Domain,
		TLSVerify: p.TLSVerify, Codecs: p.Codecs, DTMFMode: p.DTMFMode, RegisterExpiry: p.RegisterExpiry}
}

// Account decrypts a phone's credentials into a SIP account.
func (s *Service) Account(ctx context.Context, ph *store.Phone) (*sipua.Account, *store.PBX, error) {
	pbx, err := s.store.GetPBX(ctx, ph.PBXID)
	if err != nil {
		return nil, nil, err
	}
	pw, err := s.box.Open(ph.Secret, ph.SecretContext())
	if err != nil {
		return nil, nil, err
	}
	return &sipua.Account{PhoneID: ph.ID, PBX: ToSIPPBX(pbx), User: ph.SIPUser, AuthUser: ph.AuthUser, Password: string(pw),
		DisplayName: ph.DisplayName, ContactToken: ph.ContactToken}, pbx, nil
}

// SyncPBXs pushes the PBX list to the engine (source address filter, TLS policy).
func (s *Service) SyncPBXs(ctx context.Context) error {
	pbxs, err := s.store.ListPBXs(ctx)
	if err != nil {
		return err
	}
	list := make([]sipua.PBX, 0, len(pbxs))
	for _, p := range pbxs {
		if p.Enabled {
			list = append(list, ToSIPPBX(p))
		}
	}
	s.engine.SetPBXs(list)
	return nil
}

// ---- clients -----------------------------------------------------------------------

// Connect registers a new client and sends it the hello message.
func (s *Service) Connect(ctx context.Context, userID int64, username string, sessionID int64, userView any, out Outbound) *Client {
	c := &Client{svc: s, id: newClientID(), UserID: userID, Username: username, SessionID: sessionID, out: out,
		userView: userView, phones: map[int64]bool{}}
	s.mu.Lock()
	s.clients[c] = true
	s.mu.Unlock()
	phones, _ := s.phoneViews(ctx, c)
	s.mu.Lock()
	var calls []CallView
	for _, e := range s.calls {
		if e.userID == userID || e.offered[c] {
			calls = append(calls, s.viewLocked(e, c))
		}
	}
	s.mu.Unlock()
	if calls == nil {
		calls = []CallView{}
	}
	out.SendJSON(msgHello{Type: "hello", User: userView, Phones: phones, Calls: calls, ServerTime: time.Now().UTC(), Version: s.version})
	return c
}

// Disconnect removes a client: its phones go offline, its call waits for a reconnect.
func (s *Service) Disconnect(c *Client) {
	s.mu.Lock()
	if c.closed {
		s.mu.Unlock()
		return
	}
	c.closed = true
	delete(s.clients, c)
	var changed []int64
	for pid := range c.phones {
		delete(s.online[pid], c)
		changed = append(changed, pid)
	}
	var toReject []*sipua.Call
	for _, e := range s.calls {
		if e.offered[c] {
			delete(e.offered, c)
			if len(e.offered) == 0 && e.userID == 0 && e.call.State() == sipua.StateIncoming {
				toReject = append(toReject, e.call)
			}
		}
	}
	if e := c.call; e != nil {
		c.call = nil
		e.client = nil
		s.startDetachLocked(e)
	}
	s.mu.Unlock()
	for _, call := range toReject {
		call.Reject(480)
	}
	s.updateRegistrations(context.Background(), changed)
}

// startDetachLocked hangs up a call if no client re-attaches in time.
func (s *Service) startDetachLocked(e *callEntry) {
	if e.detach != nil {
		e.detach.Stop()
	}
	e.detach = time.AfterFunc(detachGrace, func() {
		s.mu.Lock()
		orphan := e.client == nil
		s.mu.Unlock()
		if orphan {
			s.log.Info("hanging up call without client", "call", e.call.ID())
			e.call.Hangup()
		}
	})
}

// DisconnectUser closes all connections of a user (disabled, deleted, access changed).
func (s *Service) DisconnectUser(userID int64, reason string) {
	for _, c := range s.clientsWhere(func(c *Client) bool { return c.UserID == userID }) {
		c.out.Close(reason)
	}
	s.hangupUserCalls(userID)
}

// DisconnectSession closes the connections that belong to one login session.
func (s *Service) DisconnectSession(sessionID int64, reason string) {
	for _, c := range s.clientsWhere(func(c *Client) bool { return c.SessionID == sessionID }) {
		c.out.Close(reason)
	}
}

// DisconnectOtherSessions closes a user's connections except those of one session.
func (s *Service) DisconnectOtherSessions(userID, keepSession int64, reason string) {
	for _, c := range s.clientsWhere(func(c *Client) bool { return c.UserID == userID && c.SessionID != keepSession }) {
		c.out.Close(reason)
	}
}

// ContactsChanged tells a user's clients (all clients for userID 0: shared contacts) to
// reload the phone book.
func (s *Service) ContactsChanged(userID int64) {
	for _, c := range s.clientsWhere(func(c *Client) bool { return userID == 0 || c.UserID == userID }) {
		c.out.SendJSON(msgContacts{Type: "contacts"})
	}
}

func (s *Service) clientsWhere(f func(*Client) bool) []*Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Client
	for c := range s.clients {
		if f(c) {
			out = append(out, c)
		}
	}
	return out
}

func (s *Service) hangupUserCalls(userID int64) {
	s.mu.Lock()
	var calls []*sipua.Call
	for _, e := range s.calls {
		if e.userID == userID {
			calls = append(calls, e.call)
		}
	}
	s.mu.Unlock()
	for _, c := range calls {
		c.Hangup()
	}
}

// Revalidate re-checks every client's phones and calls against the database; call it
// after any change to users, PBXs, phones or access. Lost access takes effect at once.
func (s *Service) Revalidate(ctx context.Context) {
	if err := s.SyncPBXs(ctx); err != nil {
		s.log.Error("sync PBXs", "err", err)
	}
	s.mu.Lock()
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	var entries []*callEntry
	for _, e := range s.calls {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	usable := map[int64]map[int64]*store.Phone{} // user -> phone ID -> phone
	allowed := func(userID, phoneID int64) *store.Phone {
		m, ok := usable[userID]
		if !ok {
			m = map[int64]*store.Phone{}
			if u, err := s.store.GetUser(ctx, userID); err == nil && !u.Disabled {
				if phones, err := s.store.UsablePhones(ctx, userID); err == nil {
					for _, p := range phones {
						m[p.ID] = p
					}
				}
			}
			usable[userID] = m
		}
		return m[phoneID]
	}

	var hangup []*sipua.Call
	changed := map[int64]bool{}
	s.mu.Lock()
	for _, c := range clients {
		for pid := range c.phones {
			if allowed(c.UserID, pid) == nil {
				delete(c.phones, pid)
				delete(s.online[pid], c)
				changed[pid] = true
			}
		}
	}
	for _, e := range entries {
		if e.userID != 0 && allowed(e.userID, e.phone.ID) == nil {
			hangup = append(hangup, e.call)
		}
		for c := range e.offered {
			if allowed(c.UserID, e.phone.ID) == nil {
				delete(e.offered, c)
			}
		}
	}
	// Phones whose settings changed must re-register with the new credentials.
	for pid := range s.online {
		changed[pid] = true
	}
	s.mu.Unlock()
	for _, c := range hangup {
		c.Hangup()
	}
	ids := make([]int64, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	s.updateRegistrations(ctx, ids)
	for _, c := range clients {
		s.sendPhones(ctx, c)
	}
}

// ---- registration ------------------------------------------------------------------

// updateRegistrations registers phones that have clients online and unregisters the rest
// (after unregisterDelay).
func (s *Service) updateRegistrations(ctx context.Context, phoneIDs []int64) {
	for _, pid := range phoneIDs {
		s.mu.Lock()
		n := len(s.online[pid])
		if t := s.unregTimers[pid]; t != nil && n > 0 {
			t.Stop()
			delete(s.unregTimers, pid)
		}
		s.mu.Unlock()

		ph, err := s.store.GetPhone(ctx, pid)
		if err != nil {
			s.engine.StopRegistration(pid)
			continue
		}
		acct, pbx, err := s.Account(ctx, ph)
		if err != nil || !pbx.Enabled || !ph.Register {
			s.engine.StopRegistration(pid)
			continue
		}
		if n > 0 {
			s.engine.SetRegistration(acct, true)
			continue
		}
		s.mu.Lock()
		if s.unregTimers[pid] == nil {
			id := pid
			s.unregTimers[pid] = time.AfterFunc(unregisterDelay, func() {
				s.mu.Lock()
				still := len(s.online[id]) == 0
				delete(s.unregTimers, id)
				s.mu.Unlock()
				if still {
					s.engine.StopRegistration(id)
				}
			})
		}
		s.mu.Unlock()
	}
}

// RegistrationChanged implements sipua.Handler.
func (s *Service) RegistrationChanged(phoneID int64, st sipua.RegState) {
	s.mu.Lock()
	s.regs[phoneID] = st
	var targets []*Client
	for c := range s.clients {
		if c.phones[phoneID] {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		s.sendPhones(context.Background(), c)
	}
}

// RegState returns a phone's registration state.
func (s *Service) RegState(phoneID int64) sipua.RegState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.regs[phoneID]; ok {
		return st
	}
	return sipua.RegState{Status: "off"}
}

// ---- views -------------------------------------------------------------------------

func (s *Service) phoneViews(ctx context.Context, c *Client) ([]PhoneView, error) {
	phones, err := s.store.UsablePhones(ctx, c.UserID)
	if err != nil {
		return []PhoneView{}, err
	}
	pbxNames := map[int64]string{}
	if pbxs, err := s.store.ListPBXs(ctx); err == nil {
		for _, p := range pbxs {
			pbxNames[p.ID] = p.Name
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PhoneView, 0, len(phones))
	for _, p := range phones {
		st, ok := s.regs[p.ID]
		if !ok {
			st = sipua.RegState{Status: "off"}
		}
		out = append(out, PhoneView{ID: p.ID, Label: p.Label, SIPUser: p.SIPUser, DisplayName: p.DisplayName, PBXID: p.PBXID,
			PBXName: pbxNames[p.PBXID], Owned: p.OwnerID == c.UserID, Register: p.Register, Reg: st, Online: c.phones[p.ID]})
	}
	return out, nil
}

func (s *Service) sendPhones(ctx context.Context, c *Client) {
	phones, err := s.phoneViews(ctx, c)
	if err == nil {
		c.out.SendJSON(msgPhones{Type: "phones", Phones: phones})
	}
}

func (s *Service) viewLocked(e *callEntry, c *Client) CallView {
	i := e.call.Info()
	return CallView{ID: i.ID, PhoneID: i.PhoneID, Direction: i.Direction, Remote: i.Remote, RemoteName: i.RemoteName,
		State: i.State, Codec: i.Codec, Hold: i.Hold, RemoteHold: i.RemoteHold, StartedAt: i.StartedAt, AnsweredAt: i.AnsweredAt,
		EndReason: i.EndReason, EndStatus: i.EndStatus, Attached: e.client == c && c != nil, Mine: e.userID == c.UserID}
}

// ---- errors ------------------------------------------------------------------------

type cmdError struct {
	code string
	msg  string
}

func (e *cmdError) Error() string { return e.msg }

func errf(code, format string, a ...any) error {
	return &cmdError{code: code, msg: fmt.Sprintf(format, a...)}
}

// ---- commands ----------------------------------------------------------------------

// Handle executes one control message from a client.
func (s *Service) Handle(ctx context.Context, c *Client, m *Inbound) {
	var err error
	var callID string
	switch m.Type {
	case "ping":
		c.out.SendJSON(map[string]string{"type": "pong"})
		return
	case "online":
		err = s.setOnline(ctx, c, m.Phones)
	case "dial":
		callID, err = s.dial(ctx, c, m.Phone, m.Number)
	case "answer":
		err = s.answer(c, m.Call)
	case "reject":
		err = s.withOffered(c, m.Call, func(e *callEntry) error { return e.call.Reject(486) })
	case "hangup":
		err = s.hangup(c, m.Call)
	case "dtmf":
		err = s.withOwn(c, m.Call, func(e *callEntry) error { return e.call.SendDTMF(m.Digits) })
	case "hold":
		err = s.withOwn(c, m.Call, func(e *callEntry) error { return e.call.SetHold(m.On) })
	case "transfer":
		err = s.transfer(ctx, c, m.Call, m.Number)
	case "attach":
		err = s.attach(c, m.Call)
	default:
		err = errf("invalid", "unknown message type")
	}
	if err != nil {
		code := "failed"
		var ce *cmdError
		if errors.As(err, &ce) {
			code = ce.code
		}
		c.out.SendJSON(msgError{Type: "error", Req: m.Req, Code: code, Error: err.Error()})
		return
	}
	if m.Req != "" {
		c.out.SendJSON(msgAck{Type: "ack", Req: m.Req, Call: callID})
	}
}

func (s *Service) setOnline(ctx context.Context, c *Client, ids []int64) error {
	if len(ids) > 20 {
		return errf("invalid", "too many phones")
	}
	want := map[int64]bool{}
	for _, id := range ids {
		if _, err := s.store.UsablePhone(ctx, c.UserID, id); err != nil {
			return errf("forbidden", "you cannot use phone %d", id)
		}
		want[id] = true
	}
	var changed []int64
	s.mu.Lock()
	for id := range c.phones {
		if !want[id] {
			delete(c.phones, id)
			delete(s.online[id], c)
			changed = append(changed, id)
		}
	}
	for id := range want {
		if !c.phones[id] {
			c.phones[id] = true
			if s.online[id] == nil {
				s.online[id] = map[*Client]bool{}
			}
			s.online[id][c] = true
			changed = append(changed, id)
		}
	}
	s.mu.Unlock()
	s.updateRegistrations(ctx, changed)
	s.sendPhones(ctx, c)
	return nil
}

func (s *Service) checkDialRules(ctx context.Context, userID, pbxID int64, number string) error {
	acc, err := s.store.AccessFor(ctx, userID, pbxID)
	if err != nil {
		return errf("forbidden", "no access to this PBX")
	}
	rules, err := dialrule.Parse(acc.DialRules)
	if err != nil {
		return errf("forbidden", "dial rules are invalid; ask your admin")
	}
	if !rules.Allowed(number) {
		return errf("forbidden", "you are not allowed to call %s", number)
	}
	return nil
}

func (s *Service) dial(ctx context.Context, c *Client, phoneID int64, number string) (string, error) {
	if !sipua.ValidDialString(number) {
		return "", errf("invalid", "invalid number")
	}
	ph, err := s.store.UsablePhone(ctx, c.UserID, phoneID)
	if err != nil {
		return "", errf("forbidden", "you cannot use this phone")
	}
	if err := s.checkDialRules(ctx, c.UserID, ph.PBXID, number); err != nil {
		return "", err
	}
	settings, _ := s.store.GetSettings(ctx)
	s.mu.Lock()
	if c.call != nil {
		s.mu.Unlock()
		return "", errf("busy", "this device is already in a call")
	}
	userCalls := 0
	for _, e := range s.calls {
		if e.userID == c.UserID {
			userCalls++
		}
	}
	total := len(s.calls)
	s.mu.Unlock()
	if userCalls >= settings.MaxCallsPerUser {
		return "", errf("limit", "you already have %d calls", userCalls)
	}
	if total >= s.MaxCalls {
		return "", errf("limit", "the server is at its call limit")
	}
	acct, pbx, err := s.Account(ctx, ph)
	if err != nil {
		return "", errf("failed", "phone configuration error")
	}
	if !pbx.Enabled {
		return "", errf("forbidden", "this PBX is disabled")
	}
	rec := &store.CallRecord{UserID: c.UserID, PhoneID: ph.ID, Username: c.Username, PhoneLabel: phoneLabel(ph), PBXName: pbx.Name,
		Direction: "out", Remote: number, StartedAt: time.Now(), Status: "calling"}
	if rec.ID, err = s.store.InsertCall(ctx, rec); err != nil {
		return "", errf("failed", "database error")
	}
	e := &callEntry{phone: ph, pbxName: pbx.Name, userID: c.UserID, offered: map[*Client]bool{}, record: rec}
	// Claim the client before dialing so a second dial from it is refused.
	s.mu.Lock()
	if c.call != nil || c.closed {
		s.mu.Unlock()
		s.failRecord(rec, "device busy")
		return "", errf("busy", "this device is already in a call")
	}
	e.client = c
	c.call = e
	s.mu.Unlock()
	call, err := s.engine.Dial(acct, number, &listener{s: s, e: e})
	if err != nil {
		s.mu.Lock()
		c.call = nil
		s.mu.Unlock()
		s.failRecord(rec, err.Error())
		return "", errf("failed", "cannot place call: %v", err)
	}
	s.mu.Lock()
	e.call = call
	s.calls[call.ID()] = e
	s.mu.Unlock()
	s.log.Info("outgoing call", "call", call.ID(), "user", c.Username, "phone", ph.ID, "to", number)
	s.pushCall(e)
	if call.State() == sipua.StateEnded {
		s.finish(e)
	}
	return call.ID(), nil
}

func phoneLabel(p *store.Phone) string {
	if p.Label != "" {
		return p.Label + " (" + p.SIPUser + ")"
	}
	return p.SIPUser
}

func (s *Service) lookup(id string) *callEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.calls[id]
	if e == nil || e.call == nil {
		return nil
	}
	return e
}

// withOwn runs f on a call that belongs to the client's user.
func (s *Service) withOwn(c *Client, id string, f func(*callEntry) error) error {
	e := s.lookup(id)
	if e == nil {
		return errf("not_found", "no such call")
	}
	s.mu.Lock()
	own := e.userID == c.UserID
	s.mu.Unlock()
	if !own {
		return errf("not_found", "no such call")
	}
	if err := f(e); err != nil {
		return errf("failed", "%v", err)
	}
	s.pushCall(e)
	return nil
}

func (s *Service) withOffered(c *Client, id string, f func(*callEntry) error) error {
	e := s.lookup(id)
	if e == nil {
		return errf("not_found", "no such call")
	}
	s.mu.Lock()
	ok := e.offered[c] && e.userID == 0
	s.mu.Unlock()
	if !ok {
		return errf("not_found", "no such call")
	}
	if err := f(e); err != nil {
		return errf("failed", "%v", err)
	}
	return nil
}

func (s *Service) answer(c *Client, id string) error {
	e := s.lookup(id)
	if e == nil {
		return errf("not_found", "no such call")
	}
	s.mu.Lock()
	switch {
	case !e.offered[c]:
		s.mu.Unlock()
		return errf("not_found", "no such call")
	case e.userID != 0:
		s.mu.Unlock()
		return errf("gone", "the call was answered on another device")
	case c.call != nil:
		s.mu.Unlock()
		return errf("busy", "this device is already in a call")
	}
	e.userID = c.UserID
	e.client = c
	c.call = e
	others := make([]*Client, 0, len(e.offered))
	for o := range e.offered {
		if o != c {
			others = append(others, o)
		}
	}
	e.offered = map[*Client]bool{}
	if e.record != nil {
		e.record.UserID, e.record.Username = c.UserID, c.Username
	}
	s.mu.Unlock()
	if err := e.call.Answer(); err != nil {
		s.mu.Lock()
		e.userID, e.client, c.call = 0, nil, nil
		s.mu.Unlock()
		return errf("gone", "%v", err)
	}
	s.log.Info("call answered", "call", id, "user", c.Username)
	// Other devices stop ringing.
	for _, o := range others {
		v := s.viewFor(e, o)
		v.State = sipua.StateEnded
		v.EndStatus, v.EndReason = "answered_elsewhere", "answered on another device"
		o.out.SendJSON(msgCall{Type: "call", Call: v})
	}
	s.pushCall(e)
	return nil
}

func (s *Service) hangup(c *Client, id string) error {
	e := s.lookup(id)
	if e == nil {
		return errf("not_found", "no such call")
	}
	s.mu.Lock()
	own := e.userID == c.UserID
	offered := e.offered[c]
	s.mu.Unlock()
	switch {
	case own:
		e.call.Hangup()
	case offered:
		// Declining on one device only stops it ringing there; the call rings on until
		// all offered devices declined.
		s.mu.Lock()
		delete(e.offered, c)
		last := len(e.offered) == 0 && e.userID == 0
		s.mu.Unlock()
		v := s.viewFor(e, c)
		v.State, v.EndStatus, v.EndReason = sipua.StateEnded, "rejected", "declined"
		c.out.SendJSON(msgCall{Type: "call", Call: v})
		if last {
			e.call.Reject(486)
		}
	default:
		return errf("not_found", "no such call")
	}
	return nil
}

func (s *Service) transfer(ctx context.Context, c *Client, id, number string) error {
	if !sipua.ValidDialString(number) {
		return errf("invalid", "invalid number")
	}
	e := s.lookup(id)
	if e == nil {
		return errf("not_found", "no such call")
	}
	if err := s.checkDialRules(ctx, c.UserID, e.phone.PBXID, number); err != nil {
		return err
	}
	return s.withOwn(c, id, func(e *callEntry) error { return e.call.Transfer(number) })
}

// attach moves a call's audio to this client: resume after reconnect, or hand over
// between the user's devices.
func (s *Service) attach(c *Client, id string) error {
	e := s.lookup(id)
	if e == nil {
		return errf("not_found", "no such call")
	}
	s.mu.Lock()
	if e.userID != c.UserID {
		s.mu.Unlock()
		return errf("not_found", "no such call")
	}
	if c.call != nil && c.call != e {
		s.mu.Unlock()
		return errf("busy", "this device is already in a call")
	}
	prev := e.client
	if prev != nil && prev != c {
		prev.call = nil
	}
	e.client = c
	c.call = e
	if e.detach != nil {
		e.detach.Stop()
		e.detach = nil
	}
	s.mu.Unlock()
	if prev != nil && prev != c {
		v := s.viewFor(e, prev)
		prev.out.SendJSON(msgCall{Type: "call", Call: v})
	}
	s.pushCall(e)
	return nil
}

// Audio forwards a binary frame from the client to its call.
func (s *Service) Audio(c *Client, frame []byte) {
	if len(frame) < 2 || frame[0] != FrameAudio || len(frame) > 1+480 {
		return
	}
	s.mu.Lock()
	e := c.call
	s.mu.Unlock()
	if e != nil && e.call != nil {
		e.call.WriteAudio(frame[1:])
	}
}

func (s *Service) viewFor(e *callEntry, c *Client) CallView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewLocked(e, c)
}

// pushCall sends a call's state to every client that should know about it.
func (s *Service) pushCall(e *callEntry) {
	s.mu.Lock()
	var targets []*Client
	for c := range s.clients {
		if (e.userID != 0 && c.UserID == e.userID) || e.offered[c] {
			targets = append(targets, c)
		}
	}
	views := make([]CallView, len(targets))
	for i, c := range targets {
		views[i] = s.viewLocked(e, c)
	}
	s.mu.Unlock()
	for i, c := range targets {
		c.out.SendJSON(msgCall{Type: "call", Call: views[i]})
	}
}

func (s *Service) failRecord(rec *store.CallRecord, reason string) {
	now := time.Now()
	rec.EndedAt, rec.Status, rec.Reason = &now, "failed", reason
	if err := s.store.UpdateCall(context.Background(), rec); err != nil {
		s.log.Error("update call record", "err", err)
	}
}

// finish records the outcome and forgets the call.
func (s *Service) finish(e *callEntry) {
	info := e.call.Info()
	s.mu.Lock()
	if s.calls[info.ID] != e {
		s.mu.Unlock()
		return
	}
	delete(s.calls, info.ID)
	if e.client != nil && e.client.call == e {
		e.client.call = nil
	}
	if e.detach != nil {
		e.detach.Stop()
	}
	rec := e.record
	s.mu.Unlock()
	if rec != nil {
		now := time.Now()
		rec.EndedAt = &now
		if !info.AnsweredAt.IsZero() {
			t := info.AnsweredAt
			rec.AnsweredAt = &t
		}
		rec.Status, rec.Reason, rec.SIPCode = info.EndStatus, info.EndReason, info.EndCode
		if rec.Direction == "in" && rec.AnsweredAt == nil && rec.Status != "rejected" {
			rec.Status = "missed"
		}
		if err := s.store.UpdateCall(context.Background(), rec); err != nil {
			s.log.Error("update call record", "err", err)
		}
	}
}

// ---- engine callbacks (sipua.Handler) ------------------------------------------------

// LookupContact implements sipua.Handler.
func (s *Service) LookupContact(token string) (*sipua.Account, bool) {
	if len(token) < 16 || len(token) > 64 {
		return nil, false
	}
	ctx := context.Background()
	ph, err := s.store.PhoneByContactToken(ctx, token)
	if err != nil {
		return nil, false
	}
	acct, pbx, err := s.Account(ctx, ph)
	if err != nil || !pbx.Enabled {
		return nil, false
	}
	return acct, true
}

// IncomingCall implements sipua.Handler: ring every idle client that is online on the
// phone and still allowed to use it.
func (s *Service) IncomingCall(call *sipua.Call) int {
	ctx := context.Background()
	ph, err := s.store.GetPhone(ctx, call.PhoneID())
	if err != nil {
		return 404
	}
	pbx, _ := s.store.GetPBX(ctx, ph.PBXID)
	users := map[int64]bool{}
	if ids, err := s.store.PhoneUsers(ctx, ph.ID); err == nil {
		for _, id := range ids {
			users[id] = true
		}
	}
	e := &callEntry{call: call, phone: ph, offered: map[*Client]bool{}}
	if pbx != nil {
		e.pbxName = pbx.Name
	}
	call.SetListener(&listener{s: s, e: e})
	s.mu.Lock()
	busy := 0
	for c := range s.online[ph.ID] {
		if !users[c.UserID] {
			continue
		}
		if c.call != nil {
			busy++
			continue
		}
		e.offered[c] = true
	}
	full := len(s.calls) >= s.MaxCalls
	n := len(e.offered)
	s.mu.Unlock()
	info := call.Info()
	rec := &store.CallRecord{PhoneID: ph.ID, PhoneLabel: phoneLabel(ph), PBXName: e.pbxName, Direction: "in",
		Remote: info.Remote, RemoteName: info.RemoteName, StartedAt: time.Now(), Status: "ringing"}
	id, err := s.store.InsertCall(ctx, rec)
	if err != nil {
		return 500
	}
	rec.ID = id
	e.record = rec
	if n == 0 || full {
		code := 480
		if busy > 0 {
			code = 486
		}
		now := time.Now()
		rec.EndedAt, rec.Status, rec.SIPCode, rec.Reason = &now, "missed", code, "no device available"
		if busy > 0 {
			rec.Reason = "all devices busy"
		}
		s.store.UpdateCall(ctx, rec)
		return code
	}
	s.mu.Lock()
	s.calls[call.ID()] = e
	s.mu.Unlock()
	s.log.Info("incoming call", "call", call.ID(), "phone", ph.ID, "from", info.Remote, "devices", len(e.offered))
	s.pushCall(e)
	return 0
}

type listener struct {
	s *Service
	e *callEntry
}

func (l *listener) CallState(c *sipua.Call) {
	l.s.mu.Lock()
	registered := l.s.calls[c.ID()] == l.e
	l.s.mu.Unlock()
	if !registered {
		return // dial() registers the entry right after Dial returns
	}
	l.s.pushCall(l.e)
	if c.State() == sipua.StateEnded {
		l.s.finish(l.e)
	}
}

func (l *listener) CallAudio(c *sipua.Call, payload []byte) {
	l.s.mu.Lock()
	client := l.e.client
	l.s.mu.Unlock()
	if client == nil {
		return
	}
	buf := make([]byte, 1+len(payload))
	buf[0] = FrameAudio
	copy(buf[1:], payload)
	client.out.SendBinary(buf)
}

func (l *listener) CallDTMF(c *sipua.Call, d byte) {
	l.s.mu.Lock()
	client := l.e.client
	l.s.mu.Unlock()
	if client != nil {
		client.out.SendJSON(msgDTMF{Type: "dtmf", Call: c.ID(), Digit: string(d)})
	}
}

// ---- admin views -------------------------------------------------------------------

// ActiveCall is a call in the admin overview.
type ActiveCall struct {
	sipua.CallInfo
	Username   string `json:"username"`
	PhoneLabel string `json:"phoneLabel"`
	PBXName    string `json:"pbxName"`
	Devices    int    `json:"devices"`
}

func (s *Service) ActiveCalls() []ActiveCall {
	s.mu.Lock()
	entries := make([]*callEntry, 0, len(s.calls))
	for _, e := range s.calls {
		entries = append(entries, e)
	}
	s.mu.Unlock()
	out := []ActiveCall{}
	for _, e := range entries {
		s.mu.Lock()
		username := ""
		if e.record != nil {
			username = e.record.Username
		}
		devices := len(e.offered)
		if e.client != nil {
			devices = 1
		}
		s.mu.Unlock()
		out = append(out, ActiveCall{CallInfo: e.call.Info(), Username: username, PhoneLabel: phoneLabel(e.phone), PBXName: e.pbxName, Devices: devices})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// HangupCall ends any call (admin).
func (s *Service) HangupCall(id string) bool {
	e := s.lookup(id)
	if e == nil {
		return false
	}
	e.call.Hangup()
	return true
}

// ConnectedClients counts connections per user ID.
func (s *Service) ConnectedClients() map[int64]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int64]int{}
	for c := range s.clients {
		out[c.UserID]++
	}
	return out
}

// OnlinePhones returns how many clients receive calls on each phone.
func (s *Service) OnlinePhones() map[int64]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int64]int{}
	for id, cs := range s.online {
		if len(cs) > 0 {
			out[id] = len(cs)
		}
	}
	return out
}
