package sipua

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/revocx35/web-ip-phone/internal/media"
)

type CallState string

const (
	StateCalling  CallState = "calling"  // outgoing INVITE sent
	StateRinging  CallState = "ringing"  // outgoing: remote is ringing
	StateEarly    CallState = "early"    // outgoing: early media (progress tones)
	StateIncoming CallState = "incoming" // incoming: waiting for a client to answer
	StateActive   CallState = "active"
	StateEnded    CallState = "ended"
)

// CallListener receives call events. Calls come from engine goroutines; must not block.
type CallListener interface {
	CallState(c *Call)
	CallAudio(c *Call, payload []byte)
	CallDTMF(c *Call, digit byte)
}

// CallInfo is a snapshot of a call.
type CallInfo struct {
	ID         string    `json:"id"`
	PhoneID    int64     `json:"phoneId"`
	Direction  string    `json:"direction"` // out | in
	Remote     string    `json:"remote"`
	RemoteName string    `json:"remoteName"`
	State      CallState `json:"state"`
	Codec      string    `json:"codec"`
	Hold       bool      `json:"hold"`
	RemoteHold bool      `json:"remoteHold"`
	StartedAt  time.Time `json:"startedAt"`
	AnsweredAt time.Time `json:"answeredAt,omitzero"`
	EndReason  string    `json:"endReason,omitempty"`
	EndCode    int       `json:"endCode,omitempty"`
	// EndStatus classifies the outcome for call history: answered, cancelled, busy,
	// rejected, failed, missed.
	EndStatus string `json:"endStatus,omitempty"`
}

type answerDecision struct {
	answer bool
	code   int
}

// Call is one SIP call with its RTP session.
type Call struct {
	e        *Engine
	id       string
	callID   string
	acct     Account
	outgoing bool
	started  time.Time

	mu         sync.Mutex
	state      CallState
	remote     string
	remoteName string
	answeredAt time.Time
	hold       bool
	remoteHold bool
	endReason  string
	endCode    int
	endStatus  string
	listener   CallListener

	media   *media.Session
	local   localSDP
	lastSDP string
	remSDP  *remoteSDP
	codec   media.Codec
	dtmfPT  uint8

	dlgC       *sipgo.DialogClientSession
	dlgS       *sipgo.DialogServerSession
	cancelDial context.CancelFunc
	answerCh   chan answerDecision
	ackCh      chan uint32 // CSeq of received ACKs (re-INVITE 2xx retransmission)

	endOnce sync.Once
	ended   chan struct{}
}

func newID() string {
	var b [12]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func randUint64() uint64 {
	var b [8]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint64(b[:]) >> 2
}

func (c *Call) ID() string { return c.id }

func (c *Call) PhoneID() int64 { return c.acct.PhoneID }

func (c *Call) Outgoing() bool { return c.outgoing }

// SetListener sets the event receiver; call it before the call can produce events
// (right after Dial, or inside Handler.IncomingCall).
func (c *Call) SetListener(l CallListener) {
	c.mu.Lock()
	c.listener = l
	c.mu.Unlock()
}

func (c *Call) Info() CallInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := "in"
	if c.outgoing {
		dir = "out"
	}
	codec := ""
	if c.state == StateActive || c.state == StateEarly {
		codec = c.codec.Name
	}
	return CallInfo{
		ID: c.id, PhoneID: c.acct.PhoneID, Direction: dir, Remote: c.remote, RemoteName: c.remoteName,
		State: c.state, Codec: codec, Hold: c.hold, RemoteHold: c.remoteHold, StartedAt: c.started,
		AnsweredAt: c.answeredAt, EndReason: c.endReason, EndCode: c.endCode, EndStatus: c.endStatus,
	}
}

func (c *Call) State() CallState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Ended is closed when the call is over.
func (c *Call) Ended() <-chan struct{} { return c.ended }

func (c *Call) notify() {
	c.mu.Lock()
	l := c.listener
	c.mu.Unlock()
	if l != nil {
		l.CallState(c)
	}
}

func (c *Call) setState(s CallState) {
	c.mu.Lock()
	if c.state == s || c.state == StateEnded {
		c.mu.Unlock()
		return
	}
	c.state = s
	if s == StateActive && c.answeredAt.IsZero() {
		c.answeredAt = time.Now()
	}
	c.mu.Unlock()
	c.notify()
}

// end finalizes the call once.
func (c *Call) end(status, reason string, code int) {
	c.endOnce.Do(func() {
		c.mu.Lock()
		c.state = StateEnded
		if status == "answered" || (status == "" && !c.answeredAt.IsZero()) {
			status = "answered"
		}
		c.endStatus, c.endReason, c.endCode = status, reason, code
		m := c.media
		c.mu.Unlock()
		if m != nil {
			m.Close()
		}
		c.e.removeCall(c)
		close(c.ended)
		c.e.log.Info("call ended", "call", c.id, "phone", c.acct.PhoneID, "status", status, "reason", reason, "code", code)
		c.notify()
	})
}

func (c *Call) newMedia() error {
	m, err := media.NewSession(c.e.cfg.RTPPool)
	if err != nil {
		return err
	}
	m.OnAudio = func(p []byte) {
		c.mu.Lock()
		l := c.listener
		c.mu.Unlock()
		if l != nil {
			l.CallAudio(c, p)
		}
	}
	m.OnDTMF = func(d byte) {
		c.mu.Lock()
		l := c.listener
		c.mu.Unlock()
		if l != nil {
			l.CallDTMF(c, d)
		}
	}
	c.media = m
	return nil
}

// applyRemote points the RTP session at the peer's SDP. Caller holds no lock.
func (c *Call) applyRemote(r *remoteSDP, codec media.Codec, dtmfPT uint8) {
	c.mu.Lock()
	c.remSDP = r
	c.codec = codec
	c.dtmfPT = dtmfPT
	c.remoteHold = r.Direction == "sendonly" || r.Direction == "inactive" || r.Port == 0
	m := c.media
	c.mu.Unlock()
	if r.Port != 0 && r.IP != nil && !r.IP.IsUnspecified() {
		m.SetRemote(&net.UDPAddr{IP: r.IP, Port: r.Port}, codec, dtmfPT)
	}
	m.Start()
}

// sdpBody renders our SDP, bumping the version when the content changed.
func (c *Call) sdpBody() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir := "sendrecv"
	if c.remSDP != nil {
		switch c.remSDP.Direction {
		case "sendonly":
			dir = "recvonly"
		case "recvonly":
			dir = "sendonly"
		case "inactive":
			dir = "inactive"
		}
	}
	if c.hold {
		switch dir {
		case "sendrecv":
			dir = "sendonly"
		case "recvonly":
			dir = "inactive"
		}
	}
	c.local.Direction = dir
	probe := c.local
	probe.Version = 0
	key := string(probe.Marshal())
	if key != c.lastSDP {
		if c.lastSDP != "" {
			c.local.Version++
		}
		c.lastSDP = key
	}
	return c.local.Marshal()
}

// Dial places an outgoing call to target from the account.
func (e *Engine) Dial(acct *Account, target string, l CallListener) (*Call, error) {
	if !ValidDialString(target) {
		return nil, errors.New("invalid number")
	}
	if e.closing.Load() {
		return nil, errors.New("server is shutting down")
	}
	client, ip, err := e.clientFor(&acct.PBX)
	if err != nil {
		return nil, err
	}
	c := &Call{
		e: e, id: newID(), callID: sip.GenerateTagN(32), acct: *acct, outgoing: true, started: time.Now(),
		state: StateCalling, remote: target, listener: l, ended: make(chan struct{}), ackCh: make(chan uint32, 4),
	}
	if err := c.newMedia(); err != nil {
		return nil, err
	}
	dtmfPT := uint8(0)
	if acct.PBX.DTMFMode != "info" {
		dtmfPT = dtmfPayloadType
	}
	c.local = localSDP{SessionID: randUint64(), Version: 1, IP: ip, Port: c.media.LocalPort(),
		Codecs: codecsFromNames(acct.PBX.Codecs), DTMFPT: dtmfPT}

	domain := acct.PBX.domain()
	ruri, route := acct.PBX.requestURI(escapeUser(target))
	req := sip.NewRequest(sip.INVITE, ruri)
	if route != nil {
		req.AppendHeader(route)
	}
	req.SetDestination(acct.PBX.hostport())
	req.SetTransport(acct.PBX.Transport)
	from := &sip.FromHeader{DisplayName: acct.DisplayName, Address: sip.Uri{Scheme: "sip", User: acct.User, Host: domain}, Params: sip.NewParams()}
	from.Params.Add("tag", sip.GenerateTagN(16))
	req.AppendHeader(from)
	req.AppendHeader(&sip.ToHeader{Address: sip.Uri{Scheme: "sip", User: escapeUser(target), Host: domain}, Params: sip.NewParams()})
	cid := sip.CallIDHeader(c.callID)
	req.AppendHeader(&cid)
	req.AppendHeader(sip.NewHeader("Allow", allowHeader))
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	req.SetBody(c.sdpBody())

	contact := e.contactFor(acct, ip)
	dua := &sipgo.DialogUA{Client: client, ContactHDR: contact}
	ctx, cancel := context.WithCancel(e.ctx)
	c.cancelDial = cancel
	e.addCall(c)
	e.work.Add(1)
	go func() {
		defer e.work.Done()
		c.runOutgoing(ctx, dua, req)
	}()
	return c, nil
}

func (c *Call) runOutgoing(ctx context.Context, dua *sipgo.DialogUA, req *sip.Request) {
	defer c.cancelDial()
	dlg, err := dua.WriteInvite(ctx, req)
	if err != nil {
		c.end("failed", "cannot reach PBX: "+err.Error(), 0)
		return
	}
	c.mu.Lock()
	c.dlgC = dlg
	c.mu.Unlock()
	defer dlg.Close()

	err = dlg.WaitAnswer(ctx, sipgo.AnswerOptions{
		Username: c.acct.authUser(),
		Password: c.acct.Password,
		OnResponse: func(res *sip.Response) error {
			switch {
			case res.StatusCode == sip.StatusRinging || res.StatusCode == sip.StatusSessionInProgress || res.StatusCode == sip.StatusQueued:
				if len(res.Body()) > 0 {
					if r, err := parseSDP(res.Body()); err == nil {
						if codec, ok := pickCodec(r.Codecs, c.local.Codecs); ok {
							c.applyRemote(r, codec, r.DTMFPT)
							c.setState(StateEarly)
							return nil
						}
					}
				}
				if c.State() != StateEarly {
					c.setState(StateRinging)
				}
			}
			return nil
		},
	})
	if err != nil {
		var derr *sipgo.ErrDialogResponse
		switch {
		case errors.As(err, &derr):
			code := derr.Res.StatusCode
			c.end(statusForCode(code), fmt.Sprintf("%d %s", code, derr.Res.Reason), code)
		case ctx.Err() != nil:
			if res := dlg.InviteResponse; res != nil && res.IsSuccess() {
				// The PBX answered while we were cancelling: complete and end the dialog.
				cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				dlg.Ack(cctx)
				dlg.Bye(cctx)
				cancel()
			}
			c.end("cancelled", "cancelled", 0)
		default:
			c.end("failed", err.Error(), 0)
		}
		return
	}

	r, err := parseSDP(dlg.InviteResponse.Body())
	var codec media.Codec
	ok := false
	if err == nil {
		codec, ok = pickCodec(r.Codecs, c.local.Codecs)
	}
	ackCtx, ackCancel := context.WithTimeout(context.Background(), 5*time.Second)
	ackErr := dlg.Ack(ackCtx)
	ackCancel()
	if !ok {
		c.e.log.Warn("answer without usable audio", "call", c.id, "err", err)
		byeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		dlg.Bye(byeCtx)
		cancel()
		c.end("failed", "no common audio codec", sip.StatusNotAcceptableHere)
		return
	}
	if ackErr != nil {
		c.end("failed", "ACK failed: "+ackErr.Error(), 0)
		return
	}
	c.applyRemote(r, codec, r.DTMFPT)
	c.setState(StateActive)
	c.watch()
}

func statusForCode(code int) string {
	switch code {
	case sip.StatusBusyHere, sip.StatusGlobalBusyEverywhere:
		return "busy"
	case sip.StatusRequestTerminated:
		return "cancelled"
	case sip.StatusGlobalDecline, sip.StatusForbidden:
		return "rejected"
	}
	return "failed"
}

// watch hangs up a call whose media stopped (dead peer) and waits for its end.
func (c *Call) watch() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.ended:
			return
		case <-c.e.ctx.Done():
			c.Hangup()
			return
		case <-t.C:
			c.mu.Lock()
			expectRTP := !c.remoteHold && c.state == StateActive
			since := c.answeredAt
			m := c.media
			c.mu.Unlock()
			if last := m.LastRx(); last.After(since) {
				since = last
			}
			if expectRTP && time.Since(since) > 90*time.Second {
				c.e.log.Warn("no RTP for 90 s, hanging up", "call", c.id)
				c.hangup("no audio from PBX (media timeout)")
				return
			}
		}
	}
}

func (e *Engine) handleNewInvite(req *sip.Request, tx sip.ServerTransaction) {
	respond := func(code int, reason string) {
		tx.Respond(sip.NewResponseFromRequest(req, code, reason, nil))
	}
	acct, ok := e.cfg.Handler.LookupContact(req.Recipient.User)
	if !ok || !e.sourceMatchesPBX(req.Source(), &acct.PBX) {
		respond(sip.StatusNotFound, "Not Found")
		return
	}
	if req.Contact() == nil || req.From() == nil {
		respond(sip.StatusBadRequest, "Bad Request")
		return
	}
	offer, err := parseSDP(req.Body())
	if err != nil {
		// Offer-less INVITEs (late offer) are not supported.
		respond(sip.StatusNotAcceptableHere, "Not Acceptable Here")
		return
	}
	codec, ok := pickCodec(codecsFromNames(acct.PBX.Codecs), offer.Codecs)
	if !ok {
		respond(sip.StatusNotAcceptableHere, "Not Acceptable Here")
		return
	}
	client, ip, err := e.clientFor(&acct.PBX)
	if err != nil {
		respond(sip.StatusServiceUnavailable, "Service Unavailable")
		return
	}
	dua := &sipgo.DialogUA{Client: client, ContactHDR: e.contactFor(acct, ip)}
	dlg, err := dua.ReadInvite(req, tx)
	if err != nil {
		respond(sip.StatusBadRequest, "Bad Request")
		return
	}
	defer dlg.Close()

	from := req.From()
	c := &Call{
		e: e, id: newID(), callID: req.CallID().Value(), acct: *acct, started: time.Now(), state: StateIncoming,
		remote: cleanRemoteText(unescapeUser(from.Address.User)), remoteName: cleanRemoteText(from.DisplayName),
		ended: make(chan struct{}), answerCh: make(chan answerDecision, 1), ackCh: make(chan uint32, 4), dlgS: dlg,
	}
	if c.remote == "" {
		c.remote = "anonymous"
	}
	if err := c.newMedia(); err != nil {
		dlg.Respond(sip.StatusServiceUnavailable, "Service Unavailable", nil)
		return
	}
	dtmfPT := uint8(0)
	if acct.PBX.DTMFMode != "info" {
		dtmfPT = offer.DTMFPT
	}
	c.local = localSDP{SessionID: randUint64(), Version: 1, IP: ip, Port: c.media.LocalPort(),
		Codecs: []media.Codec{codec}, DTMFPT: dtmfPT}
	c.remSDP = offer
	e.addCall(c)

	dlg.Respond(sip.StatusTrying, "Trying", nil)
	if code := e.cfg.Handler.IncomingCall(c); code >= 300 {
		dlg.Respond(code, reasonFor(code), nil)
		c.end("missed", reasonFor(code), code)
		return
	}
	if err := dlg.Respond(sip.StatusRinging, "Ringing", nil); err != nil {
		c.end("missed", "caller hung up", 0)
		return
	}
	timeout := time.NewTimer(3 * time.Minute)
	defer timeout.Stop()
	select {
	case d := <-c.answerCh:
		if !d.answer {
			dlg.Respond(d.code, reasonFor(d.code), nil)
			c.end("rejected", "declined", d.code)
			return
		}
	case <-dlg.Context().Done():
		c.end("missed", "caller hung up", 0)
		return
	case <-timeout.C:
		dlg.Respond(sip.StatusTemporarilyUnavailable, "Temporarily Unavailable", nil)
		c.end("missed", "not answered", sip.StatusTemporarilyUnavailable)
		return
	case <-c.ended:
		return
	case <-e.ctx.Done():
		dlg.Respond(sip.StatusServiceUnavailable, "Service Unavailable", nil)
		c.end("missed", "server shutting down", 0)
		return
	}

	c.applyRemote(offer, codec, dtmfPT)
	c.setState(StateActive)
	res := sip.NewSDPResponseFromRequest(dlg.InviteRequest, c.sdpBody())
	res.AppendHeader(sip.NewHeader("Allow", allowHeader))
	// Blocks until the ACK arrives (retransmitting the 200 OK meanwhile).
	if err := dlg.WriteResponse(res); err != nil {
		if c.State() != StateEnded {
			c.e.log.Warn("no ACK for 200 OK", "call", c.id, "err", err)
			c.hangup("no ACK from PBX")
		}
		return
	}
	go c.watch()
}

func reasonFor(code int) string {
	switch code {
	case sip.StatusBusyHere:
		return "Busy Here"
	case sip.StatusTemporarilyUnavailable:
		return "Temporarily Unavailable"
	case sip.StatusGlobalDecline:
		return "Decline"
	case sip.StatusNotFound:
		return "Not Found"
	case sip.StatusServiceUnavailable:
		return "Service Unavailable"
	}
	return "Rejected"
}

// sourceMatchesPBX checks that a request came from the PBX of the account.
func (e *Engine) sourceMatchesPBX(src string, p *PBX) bool {
	host, _, err := net.SplitHostPort(src)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if pip := net.ParseIP(p.Host); pip != nil {
		return pip.Equal(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", p.Host)
	if err != nil {
		return e.allowedSource(src)
	}
	for _, a := range ips {
		if a.Equal(ip) {
			return true
		}
	}
	return false
}

// Answer accepts an incoming call.
func (c *Call) Answer() error {
	if c.outgoing {
		return errors.New("not an incoming call")
	}
	select {
	case c.answerCh <- answerDecision{answer: true}:
		return nil
	default:
		return errors.New("call already answered or rejected")
	}
}

// Reject declines an incoming call with a SIP code (486 busy by default).
func (c *Call) Reject(code int) error {
	if c.outgoing {
		return errors.New("not an incoming call")
	}
	if code < 400 || code > 699 {
		code = sip.StatusBusyHere
	}
	select {
	case c.answerCh <- answerDecision{code: code}:
		return nil
	default:
		return errors.New("call already answered or rejected")
	}
}

// Hangup ends the call in any state.
func (c *Call) Hangup() { c.hangup("hung up") }

func (c *Call) hangup(reason string) {
	c.mu.Lock()
	state := c.state
	dlgC, dlgS := c.dlgC, c.dlgS
	cancel := c.cancelDial
	c.mu.Unlock()
	switch {
	case state == StateEnded:
		return
	case c.outgoing && state != StateActive:
		if cancel != nil {
			cancel() // WaitAnswer sends CANCEL in the background
		}
		// Don't keep the user waiting for the PBX's 487.
		c.end("cancelled", "cancelled", 0)
		return
	case !c.outgoing && state == StateIncoming:
		c.Reject(sip.StatusBusyHere)
		return
	}
	ctx, cc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cc()
	var err error
	if dlgC != nil {
		err = dlgC.Bye(ctx)
	} else if dlgS != nil {
		err = dlgS.Bye(ctx)
	}
	if err != nil {
		c.e.log.Debug("BYE failed", "call", c.id, "err", err)
	}
	c.end("answered", reason, 0)
}

// WriteAudio sends client audio (G.711 in the call's codec) to the PBX.
func (c *Call) WriteAudio(b []byte) {
	c.mu.Lock()
	m, st := c.media, c.state
	c.mu.Unlock()
	if m != nil && (st == StateActive || st == StateEarly) {
		m.WriteAudio(b)
	}
}

// Codec returns the negotiated codec name ("" before media is set up).
func (c *Call) Codec() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.codec.Name
}

// SendDTMF sends digits with RFC 4733 or SIP INFO, depending on negotiation.
func (c *Call) SendDTMF(digits string) error {
	for i := 0; i < len(digits); i++ {
		if _, ok := dtmfEventOK(digits[i]); !ok {
			return fmt.Errorf("invalid DTMF digit %q", digits[i])
		}
	}
	c.mu.Lock()
	m, pt, st := c.media, c.dtmfPT, c.state
	c.mu.Unlock()
	if st != StateActive {
		return errors.New("call is not active")
	}
	if pt != 0 {
		return m.SendDTMF(digits)
	}
	go func() {
		for i := 0; i < len(digits); i++ {
			body := fmt.Sprintf("Signal=%c\r\nDuration=160\r\n", digits[i])
			req := sip.NewRequest(sip.INFO, c.remoteTarget())
			req.AppendHeader(sip.NewHeader("Content-Type", "application/dtmf-relay"))
			req.SetBody([]byte(body))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := c.dialogDo(ctx, req)
			cancel()
			if err != nil {
				c.e.log.Debug("INFO DTMF failed", "call", c.id, "err", err)
				return
			}
		}
	}()
	return nil
}

func dtmfEventOK(d byte) (byte, bool) {
	switch {
	case d >= '0' && d <= '9', d == '*', d == '#', d >= 'A' && d <= 'D':
		return d, true
	}
	return 0, false
}

func (c *Call) remoteTarget() sip.Uri {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dlgC != nil && c.dlgC.InviteResponse != nil {
		if ct := c.dlgC.InviteResponse.Contact(); ct != nil {
			return ct.Address
		}
		return c.dlgC.InviteRequest.Recipient
	}
	if c.dlgS != nil {
		if ct := c.dlgS.InviteRequest.Contact(); ct != nil {
			return ct.Address
		}
	}
	return sip.Uri{}
}

func (c *Call) dialogDo(ctx context.Context, req *sip.Request) (*sip.Response, error) {
	c.mu.Lock()
	dlgC, dlgS := c.dlgC, c.dlgS
	c.mu.Unlock()
	if dlgC != nil {
		return dlgC.Do(ctx, req)
	}
	if dlgS != nil {
		return dlgS.Do(ctx, req)
	}
	return nil, errors.New("no dialog")
}

func (c *Call) dialogWrite(req *sip.Request) error {
	c.mu.Lock()
	dlgC, dlgS := c.dlgC, c.dlgS
	c.mu.Unlock()
	if dlgC != nil {
		return dlgC.WriteRequest(req)
	}
	if dlgS != nil {
		return dlgS.WriteRequest(req)
	}
	return errors.New("no dialog")
}

// SetHold puts the call on hold (re-INVITE with a=sendonly) or resumes it.
func (c *Call) SetHold(on bool) error {
	c.mu.Lock()
	if c.state != StateActive {
		c.mu.Unlock()
		return errors.New("call is not active")
	}
	if c.hold == on {
		c.mu.Unlock()
		return nil
	}
	c.hold = on
	c.mu.Unlock()
	c.media.SetHold(on)

	req := sip.NewRequest(sip.INVITE, c.remoteTarget())
	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	req.AppendHeader(sip.NewHeader("Allow", allowHeader))
	req.SetBody(c.sdpBody())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.dialogDo(ctx, req)
	if err == nil && res.IsSuccess() {
		ack := sip.NewRequest(sip.ACK, req.Recipient)
		err = c.dialogWrite(ack)
		if r, perr := parseSDP(res.Body()); perr == nil {
			c.mu.Lock()
			codec, dtmf := c.codec, c.dtmfPT
			c.mu.Unlock()
			c.applyRemote(r, codec, dtmf)
		}
	} else if err == nil {
		err = fmt.Errorf("PBX refused: %d %s", res.StatusCode, res.Reason)
	}
	if err != nil {
		c.mu.Lock()
		c.hold = !on
		c.mu.Unlock()
		c.media.SetHold(!on)
		return err
	}
	c.notify()
	return nil
}

// Transfer asks the PBX to transfer the call to target (blind transfer, REFER).
func (c *Call) Transfer(target string) error {
	if !ValidDialString(target) {
		return errors.New("invalid number")
	}
	if c.State() != StateActive {
		return errors.New("call is not active")
	}
	req := sip.NewRequest(sip.REFER, c.remoteTarget())
	req.AppendHeader(sip.NewHeader("Refer-To", fmt.Sprintf("<sip:%s@%s>", escapeUser(target), c.acct.PBX.domain())))
	req.AppendHeader(sip.NewHeader("Referred-By", fmt.Sprintf("<sip:%s@%s>", c.acct.User, c.acct.PBX.domain())))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.dialogDo(ctx, req)
	if err != nil {
		return err
	}
	if !res.IsSuccess() {
		return fmt.Errorf("PBX refused the transfer: %d %s", res.StatusCode, res.Reason)
	}
	// The PBX ends our leg with BYE once the transfer is done.
	return nil
}

// matchesDialog checks that an in-dialog request carries this dialog's tags.
func (c *Call) matchesDialog(req *sip.Request) bool {
	from, to := req.From(), req.To()
	if from == nil || to == nil {
		return false
	}
	ftag, _ := from.Params.Get("tag")
	ttag, _ := to.Params.Get("tag")
	c.mu.Lock()
	defer c.mu.Unlock()
	var localTag, remoteTag string
	switch {
	case c.dlgC != nil:
		localTag, _ = c.dlgC.InviteRequest.From().Params.Get("tag")
		if c.dlgC.InviteResponse != nil {
			remoteTag, _ = c.dlgC.InviteResponse.To().Params.Get("tag")
		}
	case c.dlgS != nil:
		remoteTag, _ = c.dlgS.InviteRequest.From().Params.Get("tag")
		localTag, _ = c.dlgS.InviteRequest.To().Params.Get("tag")
	default:
		return false
	}
	// A request from the peer carries the peer's tag in From and ours in To.
	return ttag == localTag && (remoteTag == "" || ftag == remoteTag)
}

func (c *Call) handleBye(req *sip.Request, tx sip.ServerTransaction) {
	c.mu.Lock()
	dlgC, dlgS := c.dlgC, c.dlgS
	c.mu.Unlock()
	var err error
	switch {
	case dlgC != nil:
		err = dlgC.ReadBye(req, tx)
	case dlgS != nil:
		err = dlgS.ReadBye(req, tx)
	default:
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil))
	}
	if err != nil {
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil))
	}
	status := ""
	if c.State() != StateActive && !c.outgoing {
		status = "missed"
	}
	c.end(status, "remote hung up", 0)
}

func (c *Call) handleAck(req *sip.Request) {
	if cs := req.CSeq(); cs != nil {
		// Keep the newest ACKs: drop the oldest when the buffer is full.
		for sent := false; !sent; {
			select {
			case c.ackCh <- cs.SeqNo:
				sent = true
			default:
				select {
				case <-c.ackCh:
				default:
				}
			}
		}
	}
	c.mu.Lock()
	dlgS := c.dlgS
	c.mu.Unlock()
	if dlgS != nil {
		dlgS.ReadAck(req, nil)
	}
}

// handleOffer answers a re-INVITE or UPDATE from the PBX (hold, codec or address change,
// session refresh).
func (c *Call) handleOffer(req *sip.Request, tx sip.ServerTransaction, invite bool) {
	if c.State() == StateEnded {
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil))
		return
	}
	var body []byte
	if len(req.Body()) > 0 {
		r, err := parseSDP(req.Body())
		if err != nil {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusNotAcceptableHere, "Not Acceptable Here", nil))
			return
		}
		c.mu.Lock()
		cur := c.codec
		dtmf := c.dtmfPT
		c.mu.Unlock()
		codec, ok := pickCodec([]media.Codec{cur}, r.Codecs)
		if !ok {
			codec, ok = pickCodec(codecsFromNames(c.acct.PBX.Codecs), r.Codecs)
		}
		if !ok {
			tx.Respond(sip.NewResponseFromRequest(req, sip.StatusNotAcceptableHere, "Not Acceptable Here", nil))
			return
		}
		if dtmf != 0 && r.DTMFPT != 0 {
			dtmf = r.DTMFPT
		}
		c.mu.Lock()
		c.local.Codecs = []media.Codec{codec}
		c.local.DTMFPT = dtmf
		c.mu.Unlock()
		c.applyRemote(r, codec, dtmf)
		body = c.sdpBody()
		c.notify()
	} else if invite {
		// Offer-less re-INVITE: our 200 OK carries the offer, the ACK the answer.
		body = c.sdpBody()
	}
	res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", body)
	if body != nil {
		res.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	}
	c.mu.Lock()
	contact := c.e.contactFor(&c.acct, c.local.IP)
	c.mu.Unlock()
	res.AppendHeader(&contact)
	if err := tx.Respond(res); err != nil {
		return
	}
	if !invite {
		return
	}
	// The TU retransmits a 2xx to INVITE until the ACK arrives (RFC 3261 §13.3.1.4).
	seq := req.CSeq().SeqNo
	interval := sip.T1
	deadline := time.After(64 * sip.T1)
	for {
		select {
		case s := <-c.ackCh:
			if s == seq {
				return
			}
		case <-time.After(interval):
			if !strings.EqualFold(req.Transport(), "udp") {
				continue
			}
			tx.Respond(res)
			interval = min(interval*2, sip.T2)
		case <-deadline:
			return
		case <-c.ended:
			return
		}
	}
}
