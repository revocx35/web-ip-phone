package sipua

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

// RegState is the registration status of a phone.
type RegState struct {
	Status  string    `json:"status"` // off | registering | registered | failed
	Error   string    `json:"error,omitempty"`
	Expires time.Time `json:"expires,omitzero"`
}

type registration struct {
	e      *Engine
	acct   Account
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.Mutex
	state RegState
	// last successful contact, for the unregister on stop
	lastContact *sip.ContactHeader
	client      *sipgo.Client
	callID      string
	fromTag     string
	cseq        uint32
}

// SetRegistration starts (or restarts with new settings) or stops the registration of a
// phone. Stopping removes only our own binding, never other devices of the same extension.
func (e *Engine) SetRegistration(acct *Account, on bool) {
	e.mu.Lock()
	old := e.regs[acct.PhoneID]
	if old != nil && on && reflect.DeepEqual(old.acct, *acct) {
		e.mu.Unlock()
		return // unchanged
	}
	delete(e.regs, acct.PhoneID)
	var r *registration
	var ctx context.Context
	if on {
		r = &registration{e: e, acct: *acct, done: make(chan struct{}),
			callID: sip.GenerateTagN(24), fromTag: sip.GenerateTagN(12), cseq: 1}
		ctx, r.cancel = context.WithCancel(e.ctx)
		e.regs[acct.PhoneID] = r
	}
	e.mu.Unlock()

	go func() {
		if old != nil {
			old.stop(true)
		}
		if r != nil {
			r.loop(ctx)
		} else if old != nil {
			e.cfg.Handler.RegistrationChanged(acct.PhoneID, RegState{Status: "off"})
		}
	}()
}

// StopRegistration removes a phone's registration (e.g. the phone was deleted).
func (e *Engine) StopRegistration(phoneID int64) {
	e.mu.Lock()
	r := e.regs[phoneID]
	delete(e.regs, phoneID)
	e.mu.Unlock()
	if r != nil {
		go r.stop(true)
	}
}

// Registration returns the current state of a phone's registration.
func (e *Engine) Registration(phoneID int64) RegState {
	e.mu.Lock()
	r := e.regs[phoneID]
	e.mu.Unlock()
	if r == nil {
		return RegState{Status: "off"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *registration) setState(st RegState) {
	r.mu.Lock()
	changed := r.state.Status != st.Status || r.state.Error != st.Error
	r.state = st
	r.mu.Unlock()
	if changed {
		r.e.cfg.Handler.RegistrationChanged(r.acct.PhoneID, st)
	}
}

func (r *registration) stop(unregister bool) {
	if r.cancel != nil {
		r.cancel()
		<-r.done
	}
	if !unregister {
		return
	}
	r.mu.Lock()
	contact := r.lastContact
	r.mu.Unlock()
	if contact == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c := contact.Clone()
	c.Params = sip.NewParams()
	c.Params.Add("expires", "0")
	if _, err := r.send(ctx, c, 0); err != nil {
		r.e.log.Debug("unregister failed", "phone", r.acct.PhoneID, "err", err)
	}
	r.mu.Lock()
	r.lastContact = nil
	r.mu.Unlock()
}

func (r *registration) loop(ctx context.Context) {
	defer close(r.done)
	backoff := 15 * time.Second
	for ctx.Err() == nil {
		r.setState(RegState{Status: "registering"})
		wait, err := r.registerOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.setState(RegState{Status: "failed", Error: err.Error()})
			r.e.log.Warn("registration failed", "phone", r.acct.PhoneID, "user", r.acct.User, "pbx", r.acct.PBX.Name, "err", err)
			wait = backoff
			backoff = min(backoff*2, 5*time.Minute)
		} else {
			backoff = 15 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// registerOnce registers and returns when to refresh.
func (r *registration) registerOnce(ctx context.Context) (time.Duration, error) {
	client, ip, err := r.e.clientFor(&r.acct.PBX)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.client = client
	r.mu.Unlock()
	contact := r.e.contactFor(&r.acct, ip)
	expiry := r.acct.PBX.RegisterExpiry
	if expiry <= 0 {
		expiry = 300
	}
	for attempt := 0; attempt < 2; attempt++ {
		rctx, cancel := context.WithTimeout(ctx, 32*time.Second)
		res, err := r.send(rctx, &contact, expiry)
		cancel()
		if err != nil {
			return 0, err
		}
		switch {
		case res.StatusCode == sip.StatusOK:
			granted := grantedExpiry(res, &contact, expiry)
			r.mu.Lock()
			r.lastContact = contact.Clone()
			r.mu.Unlock()
			r.setState(RegState{Status: "registered", Expires: time.Now().Add(time.Duration(granted) * time.Second)})
			// Refresh well before expiry; never more often than every 20 s.
			return max(time.Duration(granted)*time.Second*8/10, 20*time.Second), nil
		case res.StatusCode == sip.StatusIntervalToBrief:
			if h := res.GetHeader("Min-Expires"); h != nil {
				if v, err := strconv.Atoi(h.Value()); err == nil && v > expiry && v <= 7200 {
					expiry = v
					continue
				}
			}
			return 0, errors.New("PBX rejected the registration interval (423)")
		case res.StatusCode == sip.StatusUnauthorized || res.StatusCode == sip.StatusProxyAuthRequired:
			return 0, errors.New("authentication failed: check the SIP username and password")
		case res.StatusCode == sip.StatusForbidden:
			return 0, errors.New("forbidden (403): wrong credentials or registration not allowed")
		case res.StatusCode == sip.StatusNotFound:
			return 0, errors.New("unknown extension (404)")
		default:
			return 0, fmt.Errorf("PBX answered %d %s", res.StatusCode, res.Reason)
		}
	}
	return 0, errors.New("registration interval negotiation failed")
}

// send transmits one REGISTER (with digest authentication when challenged).
func (r *registration) send(ctx context.Context, contact *sip.ContactHeader, expiry int) (*sip.Response, error) {
	r.mu.Lock()
	client := r.client
	r.cseq++
	cseq := r.cseq
	r.mu.Unlock()
	if client == nil {
		var err error
		client, _, err = r.e.clientFor(&r.acct.PBX)
		if err != nil {
			return nil, err
		}
	}
	req := r.e.buildRegister(&r.acct, contact, expiry, r.callID, r.fromTag, cseq)
	res, err := client.Do(ctx, req, sipgo.ClientRequestBuild)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == sip.StatusUnauthorized || res.StatusCode == sip.StatusProxyAuthRequired {
		res, err = client.DoDigestAuth(ctx, req, res, sipgo.DigestAuth{Username: r.acct.authUser(), Password: r.acct.Password})
		if err != nil {
			return nil, err
		}
		// DoDigestAuth incremented the request's CSeq.
		r.mu.Lock()
		if s := req.CSeq(); s != nil && s.SeqNo > r.cseq {
			r.cseq = s.SeqNo
		}
		r.mu.Unlock()
	}
	return res, nil
}

func (e *Engine) buildRegister(a *Account, contact *sip.ContactHeader, expiry int, callID, fromTag string, cseq uint32) *sip.Request {
	domain := a.PBX.domain()
	req := sip.NewRequest(sip.REGISTER, sip.Uri{Scheme: "sip", Host: domain})
	req.SetDestination(a.PBX.hostport())
	req.SetTransport(a.PBX.Transport)
	aor := sip.Uri{Scheme: "sip", User: a.User, Host: domain}
	from := &sip.FromHeader{DisplayName: a.DisplayName, Address: aor, Params: sip.NewParams()}
	from.Params.Add("tag", fromTag)
	req.AppendHeader(from)
	req.AppendHeader(&sip.ToHeader{Address: aor, Params: sip.NewParams()})
	cid := sip.CallIDHeader(callID)
	req.AppendHeader(&cid)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: cseq, MethodName: sip.REGISTER})
	mf := sip.MaxForwardsHeader(70)
	req.AppendHeader(&mf)
	req.AppendHeader(contact.Clone())
	exp := sip.ExpiresHeader(uint32(expiry))
	req.AppendHeader(&exp)
	req.AppendHeader(sip.NewHeader("Allow", allowHeader))
	req.SetBody(nil)
	return req
}

// grantedExpiry reads the expiry the registrar granted to our contact.
func grantedExpiry(res *sip.Response, ours *sip.ContactHeader, requested int) int {
	for _, h := range res.GetHeaders("Contact") {
		c, ok := h.(*sip.ContactHeader)
		if !ok || c.Address.User != ours.Address.User {
			continue
		}
		if v, ok := c.Params.Get("expires"); ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return n
			}
		}
	}
	if h := res.GetHeader("Expires"); h != nil {
		if n, err := strconv.Atoi(h.Value()); err == nil && n > 0 {
			return n
		}
	}
	return requested
}

// CheckCredentials verifies an account's credentials without changing any binding: it sends
// a REGISTER without Contact, which only queries the registrar (RFC 3261 §10.2.3).
func (e *Engine) CheckCredentials(ctx context.Context, a *Account) error {
	client, _, err := e.clientFor(&a.PBX)
	if err != nil {
		return err
	}
	domain := a.PBX.domain()
	req := sip.NewRequest(sip.REGISTER, sip.Uri{Scheme: "sip", Host: domain})
	req.SetDestination(a.PBX.hostport())
	req.SetTransport(a.PBX.Transport)
	aor := sip.Uri{Scheme: "sip", User: a.User, Host: domain}
	from := &sip.FromHeader{DisplayName: a.DisplayName, Address: aor, Params: sip.NewParams()}
	from.Params.Add("tag", sip.GenerateTagN(12))
	req.AppendHeader(from)
	req.AppendHeader(&sip.ToHeader{Address: aor, Params: sip.NewParams()})
	req.SetBody(nil)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := client.Do(ctx, req, sipgo.ClientRequestBuild)
	if err != nil {
		return fmt.Errorf("no answer from PBX: %w", err)
	}
	if res.StatusCode == sip.StatusUnauthorized || res.StatusCode == sip.StatusProxyAuthRequired {
		res, err = client.DoDigestAuth(ctx, req, res, sipgo.DigestAuth{Username: a.authUser(), Password: a.Password})
		if err != nil {
			return fmt.Errorf("no answer from PBX: %w", err)
		}
	}
	switch res.StatusCode {
	case sip.StatusOK:
		return nil
	case sip.StatusUnauthorized, sip.StatusProxyAuthRequired, sip.StatusForbidden:
		return errors.New("the PBX rejected the credentials")
	case sip.StatusNotFound:
		return errors.New("the PBX does not know this extension")
	}
	return fmt.Errorf("PBX answered %d %s", res.StatusCode, res.Reason)
}

// Ping sends OPTIONS to a PBX and returns the round-trip time.
func (e *Engine) Ping(ctx context.Context, p *PBX) (time.Duration, int, error) {
	client, _, err := e.clientFor(p)
	if err != nil {
		return 0, 0, err
	}
	req := sip.NewRequest(sip.OPTIONS, sip.Uri{Scheme: "sip", Host: p.domain()})
	req.SetDestination(p.hostport())
	req.SetTransport(p.Transport)
	req.SetBody(nil)
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	start := time.Now()
	res, err := client.Do(ctx, req, sipgo.ClientRequestBuild)
	if err != nil {
		return 0, 0, fmt.Errorf("no answer: %w", err)
	}
	return time.Since(start), res.StatusCode, nil
}
