// Package sipua is the server-side SIP user agent: it registers virtual phones on PBXs and
// places/receives calls on their behalf, with RTP handled by package media.
package sipua

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/revocx35/web-ip-phone/internal/media"
)

// PBX is the connection information of a PBX.
type PBX struct {
	ID             int64
	Name           string
	Host           string
	Port           int
	Transport      string // udp | tcp | tls
	Domain         string
	TLSVerify      bool
	Codecs         []string
	DTMFMode       string // rfc4733 | info
	RegisterExpiry int
}

func (p *PBX) domain() string {
	if p.Domain != "" {
		return p.Domain
	}
	return p.Host
}

func (p *PBX) hostport() string {
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

// requestURI returns the URI for an out-of-dialog request to user at this PBX, plus a
// Route header when the SIP domain differs from the PBX address. Requests derived by the
// SIP stack (CANCEL, ACK) follow the Request-URI/Route, so the PBX address must be in one
// of them rather than only in the transport destination.
func (p *PBX) requestURI(user string) (sip.Uri, *sip.RouteHeader) {
	if p.Domain == "" || strings.EqualFold(p.Domain, p.Host) {
		u := sip.Uri{Scheme: "sip", User: user, Host: p.Host}
		if p.Port != defaultPort(p.Transport) {
			u.Port = p.Port
		}
		return u, nil
	}
	route := &sip.RouteHeader{Address: sip.Uri{Scheme: "sip", Host: p.Host, Port: p.Port, UriParams: sip.NewParams()}}
	route.Address.UriParams.Add("lr", "")
	if p.Transport != "udp" {
		route.Address.UriParams.Add("transport", p.Transport)
	}
	return sip.Uri{Scheme: "sip", User: user, Host: p.Domain}, route
}

func defaultPort(transport string) int {
	if transport == "tls" {
		return 5061
	}
	return 5060
}

// Account is a virtual phone: SIP credentials on a PBX.
type Account struct {
	PhoneID      int64
	PBX          PBX
	User         string // SIP user / extension
	AuthUser     string // digest username
	Password     string
	DisplayName  string
	ContactToken string // routing token in our Contact URI
}

func (a *Account) authUser() string {
	if a.AuthUser != "" {
		return a.AuthUser
	}
	return a.User
}

// Handler receives engine events. Methods are called from engine goroutines and must not block.
type Handler interface {
	// LookupContact maps the user part of a Request-URI to an account.
	LookupContact(token string) (*Account, bool)
	// IncomingCall offers a new call. Returning a SIP code >= 300 rejects it; 0 means it is
	// ringing somewhere and the handler will Answer or Reject it later.
	IncomingCall(c *Call) int
	// RegistrationChanged reports a phone's registration state.
	RegistrationChanged(phoneID int64, st RegState)
}

type Config struct {
	ListenPort  int
	AdvertiseIP net.IP
	RTPPool     *media.PortPool
	Trace       bool
	UserAgent   string
	Logger      *slog.Logger
	Handler     Handler
}

type Engine struct {
	cfg    Config
	log    *slog.Logger
	ua     *sipgo.UserAgent
	srv    *sipgo.Server
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	clients map[string]*sipgo.Client // by transport|advertiseIP
	regs    map[int64]*registration  // by phone ID
	calls   map[string]*Call         // by SIP Call-ID

	// allowed holds the PBX addresses that may send us SIP (source filter).
	allowedMu sync.RWMutex
	allowed   map[netip.Addr]bool
	pbxHosts  map[int64]string

	tlsPolicy sync.Map // server name -> verify (bool)

	udpConn net.PacketConn
	tcpLn   net.Listener
}

func New(cfg Config) (*Engine, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "WebIPPhone"
	}
	e := &Engine{
		cfg: cfg, log: cfg.Logger.With("component", "sip"),
		clients: map[string]*sipgo.Client{}, regs: map[int64]*registration{}, calls: map[string]*Call{},
		allowed: map[netip.Addr]bool{}, pbxHosts: map[int64]string{},
	}
	// sipgo's logger and trace switch are package globals read by its goroutines: set once.
	sipgoInit.Do(func() {
		sip.SIPDebug = cfg.Trace
		sip.SetDefaultLogger(cfg.Logger.With("component", "sipgo"))
	})

	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verification is done in VerifyConnection so each PBX can choose strict or
		// self-signed mode; Go's default verification would apply to every PBX alike.
		InsecureSkipVerify: true, //nolint:gosec // verified below
		VerifyConnection:   e.verifyTLS,
	}
	ua, err := sipgo.NewUA(
		sipgo.WithUserAgent(cfg.UserAgent),
		sipgo.WithUserAgenTLSConfig(tlsConf),
		sipgo.WithUserAgentTransportLayerOptions(sip.WithTransportLayerReadFilter(e.readFilter)),
	)
	if err != nil {
		return nil, err
	}
	e.ua = ua
	srv, err := sipgo.NewServer(ua)
	if err != nil {
		return nil, err
	}
	e.srv = srv
	srv.OnInvite(e.onInvite)
	srv.OnAck(e.onAck)
	srv.OnBye(e.onBye)
	srv.OnCancel(func(req *sip.Request, tx sip.ServerTransaction) {
		// A CANCEL that matched an INVITE transaction is handled by the transaction layer.
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil))
	})
	srv.OnOptions(e.onOptions)
	srv.OnInfo(e.onInfo)
	srv.OnUpdate(e.onUpdate)
	srv.OnNotify(func(req *sip.Request, tx sip.ServerTransaction) {
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil))
	})
	srv.OnNoRoute(func(req *sip.Request, tx sip.ServerTransaction) {
		res := sip.NewResponseFromRequest(req, sip.StatusMethodNotAllowed, "Method Not Allowed", nil)
		res.AppendHeader(sip.NewHeader("Allow", allowHeader))
		tx.Respond(res)
	})
	return e, nil
}

const allowHeader = "INVITE, ACK, CANCEL, BYE, OPTIONS, INFO, UPDATE, NOTIFY"

var sipgoInit sync.Once

// Start opens the UDP and TCP listeners.
func (e *Engine) Start(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)
	addr := fmt.Sprintf("0.0.0.0:%d", e.cfg.ListenPort)
	udp, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return fmt.Errorf("SIP UDP listen %s: %w", addr, err)
	}
	tcp, err := net.Listen("tcp4", addr)
	if err != nil {
		udp.Close()
		return fmt.Errorf("SIP TCP listen %s: %w", addr, err)
	}
	go func() {
		if err := e.srv.ServeUDP(udp); err != nil && e.ctx.Err() == nil {
			e.log.Error("SIP UDP listener stopped", "err", err)
		}
	}()
	go func() {
		if err := e.srv.ServeTCP(tcp); err != nil && e.ctx.Err() == nil {
			e.log.Error("SIP TCP listener stopped", "err", err)
		}
	}()
	e.udpConn, e.tcpLn = udp, tcp
	// ServeUDP registers the socket asynchronously; requests sent before that would try to
	// bind the port a second time.
	for i := 0; i < 200; i++ {
		if c, _ := e.ua.TransportLayer().GetConnection("udp", addr); c != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	go e.refreshAllowedLoop()
	e.log.Info("SIP listening", "port", e.cfg.ListenPort, "udp", true, "tcp", true)
	return nil
}

// Close hangs up all calls, unregisters and stops the listeners.
func (e *Engine) Close() {
	e.mu.Lock()
	calls := make([]*Call, 0, len(e.calls))
	for _, c := range e.calls {
		calls = append(calls, c)
	}
	regs := make([]*registration, 0, len(e.regs))
	for _, r := range e.regs {
		regs = append(regs, r)
	}
	e.mu.Unlock()
	for _, c := range calls {
		c.Hangup()
	}
	var wg sync.WaitGroup
	for _, r := range regs {
		wg.Add(1)
		go func() { defer wg.Done(); r.stop(true) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	if e.cancel != nil {
		e.cancel()
	}
	if e.udpConn != nil {
		e.udpConn.Close()
		e.tcpLn.Close()
	}
	e.ua.Close()
}

// SetPBXs updates the list of PBX hosts allowed to send us SIP.
func (e *Engine) SetPBXs(pbxs []PBX) {
	hosts := map[int64]string{}
	for _, p := range pbxs {
		hosts[p.ID] = p.Host
		e.tlsPolicy.Store(p.Host, p.TLSVerify)
	}
	e.allowedMu.Lock()
	e.pbxHosts = hosts
	e.allowedMu.Unlock()
	e.refreshAllowed()
}

func (e *Engine) refreshAllowedLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
			e.refreshAllowed()
		}
	}
}

// refreshAllowed resolves PBX host names to the addresses we accept SIP from.
func (e *Engine) refreshAllowed() {
	e.allowedMu.RLock()
	hosts := make([]string, 0, len(e.pbxHosts))
	for _, h := range e.pbxHosts {
		hosts = append(hosts, h)
	}
	e.allowedMu.RUnlock()
	allowed := map[netip.Addr]bool{}
	for _, h := range hosts {
		if a, err := netip.ParseAddr(h); err == nil {
			allowed[a.Unmap()] = true
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		cancel()
		if err != nil {
			e.log.Warn("cannot resolve PBX host", "host", h, "err", err)
			continue
		}
		for _, a := range ips {
			allowed[a.Unmap()] = true
		}
	}
	e.allowedMu.Lock()
	e.allowed = allowed
	e.allowedMu.Unlock()
}

// readFilter drops SIP traffic from anything but a configured PBX.
func (e *Engine) readFilter(info sip.TransportReadProps, data []byte) ([]byte, error) {
	var ip net.IP
	switch a := info.RemoteAddr.(type) {
	case *net.UDPAddr:
		ip = a.IP
	case *net.TCPAddr:
		ip = a.IP
	}
	addr, ok := netip.AddrFromSlice(ip)
	if ok {
		e.allowedMu.RLock()
		ok = e.allowed[addr.Unmap()]
		e.allowedMu.RUnlock()
	}
	if !ok {
		if info.Transport == "udp" {
			return nil, nil // silently drop, don't kill the listener
		}
		return nil, errors.New("SIP connection from unknown address")
	}
	return data, nil
}

func (e *Engine) allowedSource(src string) bool {
	host, _, err := net.SplitHostPort(src)
	if err != nil {
		return false
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	e.allowedMu.RLock()
	defer e.allowedMu.RUnlock()
	return e.allowed[a.Unmap()]
}

func (e *Engine) verifyTLS(cs tls.ConnectionState) error {
	verify := true
	if v, ok := e.tlsPolicy.Load(cs.ServerName); ok {
		verify = v.(bool)
	}
	if !verify {
		return nil
	}
	if len(cs.PeerCertificates) == 0 {
		return errors.New("PBX sent no certificate")
	}
	opts := x509.VerifyOptions{DNSName: cs.ServerName, Intermediates: x509.NewCertPool()}
	for _, c := range cs.PeerCertificates[1:] {
		opts.Intermediates.AddCert(c)
	}
	_, err := cs.PeerCertificates[0].Verify(opts)
	return err
}

// localIPFor returns the address we advertise to a PBX.
func (e *Engine) localIPFor(p *PBX) (net.IP, error) {
	if e.cfg.AdvertiseIP != nil {
		return e.cfg.AdvertiseIP, nil
	}
	c, err := net.DialTimeout("udp4", p.hostport(), 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("no route to PBX %s: %w", p.Host, err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP, nil
}

// clientFor returns a sipgo client whose Via/Contact carry our address towards the PBX.
func (e *Engine) clientFor(p *PBX) (*sipgo.Client, net.IP, error) {
	ip, err := e.localIPFor(p)
	if err != nil {
		return nil, nil, err
	}
	key := p.Transport + "|" + ip.String()
	e.mu.Lock()
	defer e.mu.Unlock()
	if c := e.clients[key]; c != nil {
		return c, ip, nil
	}
	opts := []sipgo.ClientOption{sipgo.WithClientHostname(ip.String()), sipgo.WithClientPort(e.cfg.ListenPort)}
	if p.Transport == "udp" {
		// Send from the listening socket so the PBX's replies and requests reach it.
		opts = append(opts, sipgo.WithClientConnectionAddr(fmt.Sprintf("0.0.0.0:%d", e.cfg.ListenPort)))
	}
	c, err := sipgo.NewClient(e.ua, opts...)
	if err != nil {
		return nil, nil, err
	}
	e.clients[key] = c
	return c, ip, nil
}

func (e *Engine) contactFor(a *Account, ip net.IP) sip.ContactHeader {
	u := sip.Uri{Scheme: "sip", User: a.ContactToken, Host: ip.String(), Port: e.cfg.ListenPort}
	if a.PBX.Transport != "udp" {
		u.UriParams = sip.NewParams()
		u.UriParams.Add("transport", a.PBX.Transport)
	}
	return sip.ContactHeader{Address: u}
}

// escapeUser percent-encodes characters that may not appear raw in a SIP URI user part.
// Dial strings are validated to [0-9A-Za-z*#+._-] before reaching here.
func escapeUser(s string) string {
	return strings.ReplaceAll(s, "#", "%23")
}

func unescapeUser(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "%23", "#"), "%2A", "*")
}

func (e *Engine) addCall(c *Call) {
	e.mu.Lock()
	e.calls[c.callID] = c
	e.mu.Unlock()
}

func (e *Engine) removeCall(c *Call) {
	e.mu.Lock()
	if e.calls[c.callID] == c {
		delete(e.calls, c.callID)
	}
	e.mu.Unlock()
}

func (e *Engine) callByID(callID string) *Call {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[callID]
}

// ActiveCalls returns the number of calls in progress.
func (e *Engine) ActiveCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *Engine) onOptions(req *sip.Request, tx sip.ServerTransaction) {
	res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
	res.AppendHeader(sip.NewHeader("Allow", allowHeader))
	res.AppendHeader(sip.NewHeader("Accept", "application/sdp"))
	tx.Respond(res)
}

func (e *Engine) onAck(req *sip.Request, tx sip.ServerTransaction) {
	if id := req.CallID(); id != nil {
		if c := e.callByID(id.Value()); c != nil {
			c.handleAck(req)
		}
	}
}

func (e *Engine) onBye(req *sip.Request, tx sip.ServerTransaction) {
	id := req.CallID()
	var c *Call
	if id != nil {
		c = e.callByID(id.Value())
	}
	if c == nil || !c.matchesDialog(req) {
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil))
		return
	}
	c.handleBye(req, tx)
}

func (e *Engine) onInfo(req *sip.Request, tx sip.ServerTransaction) {
	id := req.CallID()
	if id == nil || e.callByID(id.Value()) == nil {
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil))
		return
	}
	tx.Respond(sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil))
}

func (e *Engine) onUpdate(req *sip.Request, tx sip.ServerTransaction) {
	id := req.CallID()
	var c *Call
	if id != nil {
		c = e.callByID(id.Value())
	}
	if c == nil || !c.matchesDialog(req) {
		tx.Respond(sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil))
		return
	}
	c.handleOffer(req, tx, false)
}

func (e *Engine) onInvite(req *sip.Request, tx sip.ServerTransaction) {
	if to := req.To(); to != nil {
		if tag, _ := to.Params.Get("tag"); tag != "" {
			// re-INVITE within a dialog
			id := req.CallID()
			var c *Call
			if id != nil {
				c = e.callByID(id.Value())
			}
			if c == nil || !c.matchesDialog(req) {
				tx.Respond(sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil))
				return
			}
			c.handleOffer(req, tx, true)
			return
		}
	}
	e.handleNewInvite(req, tx)
}
