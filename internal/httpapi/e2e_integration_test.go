//go:build integration

// Full-stack test: REST API + WebSocket + SIP engine against the disposable Asterisk of
// test/asterisk (see test/README.md). Two users call each other through the server.
package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/config"
	"github.com/revocx35/web-ip-phone/internal/media"
	"github.com/revocx35/web-ip-phone/internal/phone"
	"github.com/revocx35/web-ip-phone/internal/secretbox"
	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
)

type stack struct {
	srv   *httptest.Server
	api   *Server
	token string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, err := store.Open(t.TempDir() + "/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	box, _ := secretbox.New(key)
	svc := phone.New(st, box, log, "test")
	eng, err := sipua.New(sipua.Config{ListenPort: 5073, RTPPool: media.NewPortPool(24000, 24100, nil), Logger: log, Handler: svc})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetEngine(eng)
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{SIPPort: 5073, RTPPortMin: 24000, RTPPortMax: 24100}
	token := "TEST-SETU-PTOK-ENAB-CDEF"
	fast := auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1}
	api := New(Options{Config: cfg, Store: st, Box: box, Service: svc, SIP: eng, Logger: log, Version: "test",
		Static: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}, SetupToken: token, PWParams: &fast})
	srv := httptest.NewTLSServer(api.Handler())
	t.Cleanup(func() {
		srv.Close()
		eng.Close()
		st.Close()
	})
	return &stack{srv: srv, api: api, token: token}
}

type apiClient struct {
	t      *testing.T
	base   string
	hc     *http.Client
	bearer string
}

func (s *stack) client() *apiClient {
	jar, _ := cookiejar.New(nil)
	return &apiClient{base: s.srv.URL, hc: &http.Client{Jar: jar, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
}

func (c *apiClient) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	} else {
		req.Header.Set("X-Requested-With", "webphone")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
	if res.StatusCode >= 300 {
		t.Logf("%s %s -> %d %s", method, path, res.StatusCode, data)
	}
	return res.StatusCode
}

func (c *apiClient) must(t *testing.T, method, path string, body any, out any) {
	t.Helper()
	if st := c.do(t, method, path, body, out); st >= 300 {
		t.Fatalf("%s %s: status %d", method, path, st)
	}
}

type wsc struct {
	c     *websocket.Conn
	mu    sync.Mutex
	msgs  []map[string]any
	notif chan struct{}
	audio chan []byte
}

func (c *apiClient) ws(t *testing.T, origin string) (*wsc, int) {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	if c.bearer != "" {
		h.Set("Authorization", "Bearer "+c.bearer)
	}
	u := strings.Replace(c.base, "https://", "wss://", 1) + "/api/v1/ws"
	conn, res, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPClient: c.hc, HTTPHeader: h})
	if err != nil {
		if res != nil {
			return nil, res.StatusCode
		}
		t.Fatal(err)
	}
	w := &wsc{c: conn, notif: make(chan struct{}, 1), audio: make(chan []byte, 1000)}
	go func() {
		for {
			typ, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				select {
				case w.audio <- data:
				default:
				}
				continue
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			w.mu.Lock()
			w.msgs = append(w.msgs, m)
			w.mu.Unlock()
			select {
			case w.notif <- struct{}{}:
			default:
			}
		}
	}()
	t.Cleanup(func() { conn.CloseNow() })
	return w, 101
}

func (w *wsc) send(t *testing.T, v any) {
	b, _ := json.Marshal(v)
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

// wait returns the first message (after index from) matching f.
func (w *wsc) wait(t *testing.T, what string, timeout time.Duration, f func(m map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	seen := 0
	for {
		w.mu.Lock()
		for ; seen < len(w.msgs); seen++ {
			if f(w.msgs[seen]) {
				m := w.msgs[seen]
				w.msgs = w.msgs[seen+1:]
				w.mu.Unlock()
				return m
			}
		}
		w.mu.Unlock()
		select {
		case <-w.notif:
		case <-deadline:
			w.mu.Lock()
			defer w.mu.Unlock()
			t.Fatalf("timeout waiting for %s; got %v", what, w.msgs)
		}
	}
}

func isType(typ string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == typ }
}

func callState(state string) func(map[string]any) bool {
	return func(m map[string]any) bool {
		c, _ := m["call"].(map[string]any)
		return m["type"] == "call" && c != nil && c["state"] == state
	}
}

func tone(n int, codec media.Codec) [][]byte {
	var out [][]byte
	ph := 0.0
	for i := 0; i < n; i++ {
		f := make([]byte, 1+160)
		f[0] = phone.FrameAudio
		for j := 1; j <= 160; j++ {
			f[j] = codec.Encode(int16(8000 * math.Sin(ph)))
			ph += 2 * math.Pi * 800 / 8000
		}
		out = append(out, f)
	}
	return out
}

// loudFrames counts received audio frames with a clear signal.
func loudFrames(ch chan []byte, codec media.Codec, d time.Duration) (total, loud int) {
	deadline := time.After(d)
	for {
		select {
		case f := <-ch:
			total++
			var sum float64
			for _, b := range f[1:] {
				v := float64(codec.Decode(b))
				sum += v * v
			}
			if math.Sqrt(sum/float64(len(f)-1)) > 1000 {
				loud++
			}
		case <-deadline:
			return
		}
	}
}

func TestEndToEnd(t *testing.T) {
	s := newStack(t)
	origin := s.srv.URL

	// ---- first-run setup
	admin := s.client()
	var setupStatus map[string]bool
	admin.must(t, "GET", "/api/v1/setup", nil, &setupStatus)
	if !setupStatus["required"] {
		t.Fatal("setup should be required")
	}
	admin.must(t, "POST", "/api/v1/setup", map[string]string{"token": strings.ToLower(s.token), "username": "admin", "password": "Adm1n-Password!"}, nil)

	// ---- admin: PBX, extension, users, access
	var pbx store.PBX
	admin.must(t, "POST", "/api/v1/admin/pbxs", map[string]any{"name": "Test PBX", "host": pbxHost(), "port": 5160, "transport": "udp",
		"enabled": true, "codecs": []string{"PCMU", "PCMA"}}, &pbx)
	var ext store.Phone
	if st := admin.do(t, "POST", fmt.Sprintf("/api/v1/admin/pbxs/%d/phones", pbx.ID), map[string]any{"label": "Front desk",
		"sipUser": "2001", "password": "wrong-pass", "displayName": "Front Desk", "register": true, "verify": true}, nil); st != 422 {
		t.Fatalf("wrong SIP password accepted: %d", st)
	}
	admin.must(t, "POST", fmt.Sprintf("/api/v1/admin/pbxs/%d/phones", pbx.ID), map[string]any{"label": "Front desk",
		"sipUser": "2001", "password": "Test-2001-pw", "displayName": "Front Desk", "register": true, "verify": true}, &ext)
	var alice, bob store.User
	admin.must(t, "POST", "/api/v1/admin/users", map[string]any{"username": "alice", "password": "Wonder-land-42"}, &alice)
	admin.must(t, "POST", "/api/v1/admin/users", map[string]any{"username": "bob", "password": "Builder-Pass-77"}, &bob)
	admin.must(t, "PUT", fmt.Sprintf("/api/v1/admin/users/%d/access", alice.ID),
		[]map[string]any{{"pbxId": pbx.ID, "mode": "selected", "phoneIds": []int64{ext.ID}, "dialRules": "_6XX\n_200X\n-_699"}}, nil)
	admin.must(t, "PUT", fmt.Sprintf("/api/v1/admin/users/%d/access", bob.ID), []map[string]any{{"pbxId": pbx.ID, "mode": "any"}}, nil)

	// ---- alice (browser, cookie) cannot add phones in "selected" mode
	ac := s.client()
	ac.must(t, "POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "Wonder-land-42"}, nil)
	if st := ac.do(t, "POST", "/api/v1/phones", map[string]any{"pbxId": pbx.ID, "sipUser": "2003", "password": "Test-2003-pw"}, nil); st != 403 {
		t.Fatalf("alice added a phone: %d", st)
	}
	if st := ac.do(t, "GET", "/api/v1/admin/users", nil, nil); st != 403 {
		t.Fatalf("alice reached admin API: %d", st)
	}

	// ---- bob (app, bearer) adds his own phone 2002
	bc := s.client()
	var login struct {
		Token string `json:"token"`
	}
	bc.must(t, "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "Builder-Pass-77", "client": "app", "deviceName": "Pixel test"}, &login)
	bc.bearer = login.Token
	var bobPhone store.Phone
	bc.must(t, "POST", "/api/v1/phones", map[string]any{"pbxId": pbx.ID, "label": "Bob", "sipUser": "2002", "password": "Test-2002-pw",
		"displayName": "Bob Mobile", "register": true, "verify": true}, &bobPhone)

	// ---- WebSocket security
	if _, st := ac.ws(t, "https://evil.example"); st != 403 {
		t.Fatalf("cross-origin WebSocket accepted: %d", st)
	}
	if _, st := s.client().ws(t, origin); st != 401 {
		t.Fatalf("anonymous WebSocket accepted: %d", st)
	}

	aw, _ := ac.ws(t, origin)
	hello := aw.wait(t, "alice hello", 5*time.Second, isType("hello"))
	if phones := hello["phones"].([]any); len(phones) != 1 {
		t.Fatalf("alice phones: %v", phones)
	}
	bw, _ := bc.ws(t, "")
	bw.wait(t, "bob hello", 5*time.Second, isType("hello"))

	// alice may not go online with bob's phone
	aw.send(t, map[string]any{"type": "online", "phones": []int64{bobPhone.ID}, "req": "x1"})
	if m := aw.wait(t, "forbidden", 5*time.Second, isType("error")); m["code"] != "forbidden" {
		t.Fatalf("expected forbidden, got %v", m)
	}

	bw.send(t, map[string]any{"type": "online", "phones": []int64{bobPhone.ID}})
	bw.wait(t, "bob registered", 10*time.Second, func(m map[string]any) bool {
		if m["type"] != "phones" {
			return false
		}
		for _, p := range m["phones"].([]any) {
			pm := p.(map[string]any)
			if pm["reg"].(map[string]any)["status"] == "registered" {
				return true
			}
		}
		return false
	})

	// ---- dial rules
	aw.send(t, map[string]any{"type": "dial", "phone": ext.ID, "number": "699", "req": "d0"})
	if m := aw.wait(t, "dial rule", 5*time.Second, isType("error")); m["code"] != "forbidden" {
		t.Fatalf("699 should be denied: %v", m)
	}
	aw.send(t, map[string]any{"type": "dial", "phone": ext.ID, "number": "5555", "req": "d1"})
	if m := aw.wait(t, "dial rule", 5*time.Second, isType("error")); m["code"] != "forbidden" {
		t.Fatalf("5555 should be denied: %v", m)
	}

	// ---- alice calls bob
	aw.send(t, map[string]any{"type": "dial", "phone": ext.ID, "number": "2002", "req": "d2"})
	ack := aw.wait(t, "dial ack", 5*time.Second, isType("ack"))
	callID := ack["call"].(string)
	in := bw.wait(t, "bob incoming", 10*time.Second, callState("incoming"))
	inCall := in["call"].(map[string]any)
	if inCall["remote"] != "2001" || inCall["remoteName"] != "Alice" { // Asterisk applies the endpoint's callerid
		t.Fatalf("caller id: %v", inCall)
	}
	aw.wait(t, "alice ringing", 5*time.Second, callState("ringing"))
	bw.send(t, map[string]any{"type": "answer", "call": inCall["id"], "req": "a1"})
	bw.wait(t, "bob active", 10*time.Second, callState("active"))
	act := aw.wait(t, "alice active", 10*time.Second, callState("active"))
	codec, _ := media.CodecByName(act["call"].(map[string]any)["codec"].(string))

	// audio both ways through PBX
	go func() {
		for _, f := range tone(100, codec) {
			aw.c.Write(context.Background(), websocket.MessageBinary, f)
			bw.c.Write(context.Background(), websocket.MessageBinary, f)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	var wg sync.WaitGroup
	var aTot, aLoud, bTot, bLoud int
	wg.Add(2)
	go func() { defer wg.Done(); aTot, aLoud = loudFrames(aw.audio, codec, 2500*time.Millisecond) }()
	go func() { defer wg.Done(); bTot, bLoud = loudFrames(bw.audio, codec, 2500*time.Millisecond) }()
	wg.Wait()
	t.Logf("alice received %d frames (%d loud), bob %d (%d loud)", aTot, aLoud, bTot, bLoud)
	if aLoud < 50 || bLoud < 50 {
		t.Fatal("audio did not flow both ways")
	}

	// DTMF + hold on the live call
	aw.send(t, map[string]any{"type": "dtmf", "call": callID, "digits": "12#", "req": "t1"})
	aw.wait(t, "dtmf ack", 5*time.Second, isType("ack"))
	aw.send(t, map[string]any{"type": "hold", "call": callID, "on": true, "req": "h1"})
	aw.wait(t, "hold ack", 10*time.Second, isType("ack"))

	// bob may not hang up alice's call by id... he can hang up his own leg
	bw.send(t, map[string]any{"type": "hangup", "call": inCall["id"]})
	bw.wait(t, "bob ended", 10*time.Second, callState("ended"))
	aw.wait(t, "alice ended", 10*time.Second, callState("ended"))

	// ---- reconnect / handover: call echo, drop the socket, re-attach from a new one
	aw.send(t, map[string]any{"type": "dial", "phone": ext.ID, "number": "600", "req": "d3"})
	echoID := aw.wait(t, "dial ack", 5*time.Second, isType("ack"))["call"].(string)
	aw.wait(t, "echo active", 10*time.Second, callState("active"))
	aw.c.CloseNow()
	aw2, _ := ac.ws(t, origin)
	h2 := aw2.wait(t, "hello 2", 5*time.Second, isType("hello"))
	if calls := h2["calls"].([]any); len(calls) != 1 || calls[0].(map[string]any)["id"] != echoID {
		t.Fatalf("active call not offered for re-attach: %v", h2["calls"])
	}
	aw2.send(t, map[string]any{"type": "attach", "call": echoID, "req": "r1"})
	aw2.wait(t, "attach ack", 5*time.Second, isType("ack"))
	go func() {
		for _, f := range tone(60, codec) {
			aw2.c.Write(context.Background(), websocket.MessageBinary, f)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	if _, loud := loudFrames(aw2.audio, codec, 1500*time.Millisecond); loud < 30 {
		t.Fatalf("no echo after re-attach (%d loud frames)", loud)
	}

	// ---- admin revokes alice's access: the call ends at once
	admin.must(t, "PUT", fmt.Sprintf("/api/v1/admin/users/%d/access", alice.ID), []map[string]any{}, nil)
	aw2.wait(t, "call ended after revoke", 10*time.Second, callState("ended"))
	var phones struct {
		Phones []any `json:"phones"`
	}
	ac.must(t, "GET", "/api/v1/phones", nil, &phones)
	if len(phones.Phones) != 0 {
		t.Fatalf("alice still sees phones: %v", phones.Phones)
	}

	// ---- history and audit
	var calls []store.CallRecord
	ac.must(t, "GET", "/api/v1/calls", nil, &calls)
	if len(calls) < 2 {
		t.Fatalf("alice history: %+v", calls)
	}
	var bcalls []store.CallRecord
	bc.must(t, "GET", "/api/v1/calls", nil, &bcalls)
	if len(bcalls) != 1 || bcalls[0].Direction != "in" || bcalls[0].Status != "answered" {
		t.Fatalf("bob history: %+v", bcalls)
	}
	var audit []store.AuditEntry
	admin.must(t, "GET", "/api/v1/admin/audit", nil, &audit)
	actions := map[string]bool{}
	for _, e := range audit {
		actions[e.Action] = true
	}
	for _, a := range []string{"setup", "login", "pbx.create", "extension.create", "user.create", "user.access", "phone.create"} {
		if !actions[a] {
			t.Errorf("audit log lacks %s", a)
		}
	}

	// ---- disabling bob closes his connection
	admin.must(t, "PATCH", fmt.Sprintf("/api/v1/admin/users/%d", bob.ID), map[string]any{"disabled": true}, nil)
	if st := bc.do(t, "GET", "/api/v1/me", nil, nil); st != 401 {
		t.Fatalf("disabled user still signed in: %d", st)
	}
}

func pbxHost() string {
	if h := os.Getenv("WEBPHONE_TEST_PBX_HOST"); h != "" {
		return h
	}
	return "127.0.0.1"
}
