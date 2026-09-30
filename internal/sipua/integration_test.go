//go:build integration

// Integration tests against the disposable Asterisk of test/asterisk (see test/README.md):
//
//	docker build -t webphone-test-asterisk test/asterisk
//	docker run -d --name webphone-test-pbx --network host webphone-test-asterisk
//	go test -tags integration ./internal/sipua/ -v
package sipua

import (
	"context"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revocx35/web-ip-phone/internal/media"
)

func pbxHost() string {
	if h := os.Getenv("WEBPHONE_TEST_PBX_HOST"); h != "" {
		return h
	}
	return "127.0.0.1"
}

type testHandler struct {
	mu       sync.Mutex
	accounts map[string]*Account
	incoming chan *Call
	regs     map[int64]RegState
	regCh    chan int64
	listener func() CallListener
}

func (h *testHandler) LookupContact(token string) (*Account, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	a, ok := h.accounts[token]
	return a, ok
}

func (h *testHandler) IncomingCall(c *Call) int {
	c.SetListener(h.listener())
	select {
	case h.incoming <- c:
		return 0
	default:
		return 486
	}
}

func (h *testHandler) RegistrationChanged(id int64, st RegState) {
	h.mu.Lock()
	h.regs[id] = st
	h.mu.Unlock()
	select {
	case h.regCh <- id:
	default:
	}
}

type recorder struct {
	mu     sync.Mutex
	states []CallState
	stCh   chan CallState
	audio  []int16
	codec  media.Codec
	dtmf   []byte
}

func newRecorder() *recorder { return &recorder{stCh: make(chan CallState, 32)} }

func (r *recorder) CallState(c *Call) {
	st := c.State()
	r.mu.Lock()
	r.states = append(r.states, st)
	r.mu.Unlock()
	select {
	case r.stCh <- st:
	default:
	}
}

func (r *recorder) CallAudio(c *Call, p []byte) {
	codec, _ := media.CodecByName(c.Codec())
	r.mu.Lock()
	for _, b := range p {
		r.audio = append(r.audio, codec.Decode(b))
	}
	r.mu.Unlock()
}

func (r *recorder) CallDTMF(c *Call, d byte) {
	r.mu.Lock()
	r.dtmf = append(r.dtmf, d)
	r.mu.Unlock()
}

func (r *recorder) waitState(t *testing.T, want CallState, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case st := <-r.stCh:
			if st == want {
				return
			}
		case <-deadline:
			r.mu.Lock()
			defer r.mu.Unlock()
			t.Fatalf("state %s not reached, got %v", want, r.states)
		}
	}
}

// rms of the last n samples, in dBFS.
func (r *recorder) levelDB(from int) (float64, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if from >= len(r.audio) {
		return -120, 0
	}
	var sum float64
	s := r.audio[from:]
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	rms := math.Sqrt(sum / float64(len(s)))
	if rms < 1 {
		return -120, len(s)
	}
	return 20 * math.Log10(rms/32768), len(s)
}

func (r *recorder) samples() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.audio)
}

func toneFrame(codec media.Codec, phase *float64) []byte {
	out := make([]byte, media.FrameSamples)
	for i := range out {
		out[i] = codec.Encode(int16(8000 * math.Sin(*phase)))
		*phase += 2 * math.Pi * 1000 / 8000
	}
	return out
}

func newTestEngine(t *testing.T) (*Engine, *testHandler) {
	t.Helper()
	h := &testHandler{accounts: map[string]*Account{}, incoming: make(chan *Call, 4), regs: map[int64]RegState{},
		regCh: make(chan int64, 16), listener: func() CallListener { return newRecorder() }}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	e, err := New(Config{ListenPort: 5071, RTPPool: media.NewPortPool(22000, 22100, nil), Handler: h, Logger: log,
		Trace: os.Getenv("SIP_TRACE") != ""})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.SetPBXs([]PBX{testPBX()})
	t.Cleanup(e.Close)
	return e, h
}

func testPBX() PBX {
	return PBX{ID: 1, Name: "test", Host: pbxHost(), Port: 5160, Transport: "udp", Codecs: []string{"PCMU", "PCMA"},
		DTMFMode: "rfc4733", RegisterExpiry: 120}
}

func account(ext string, id int64) *Account {
	return &Account{PhoneID: id, PBX: testPBX(), User: ext, Password: "Test-" + ext + "-pw", DisplayName: "Test " + ext,
		ContactToken: "tok" + ext + "x"}
}

func astCLI(t *testing.T, cmd string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", "webphone-test-pbx", "asterisk", "-rx", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("asterisk -rx %q: %v %s", cmd, err, out)
	}
	return string(out)
}

func dial(t *testing.T, e *Engine, a *Account, target string) (*Call, *recorder) {
	t.Helper()
	rec := newRecorder()
	c, err := e.Dial(a, target, rec)
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

func TestEchoCall(t *testing.T) {
	e, _ := newTestEngine(t)
	c, rec := dial(t, e, account("2001", 1), "600")
	rec.waitState(t, StateActive, 10*time.Second)
	if c.Codec() != "PCMU" {
		t.Fatalf("codec %s", c.Codec())
	}
	codec := media.PCMU
	var phase float64
	start := rec.samples()
	for i := 0; i < 100; i++ { // 2 s of 1 kHz tone
		c.WriteAudio(toneFrame(codec, &phase))
		time.Sleep(20 * time.Millisecond)
	}
	lvl, n := rec.levelDB(start + 4000)
	t.Logf("echo level %.1f dBFS over %d samples", lvl, n)
	if n < 4000 || lvl < -30 {
		t.Fatalf("no echo: %.1f dBFS, %d samples", lvl, n)
	}
	c.Hangup()
	rec.waitState(t, StateEnded, 5*time.Second)
	if info := c.Info(); info.EndStatus != "answered" {
		t.Fatalf("end status %q", info.EndStatus)
	}
}

func TestToneAndRemoteHangupAndAlaw(t *testing.T) {
	e, _ := newTestEngine(t)
	c, rec := dial(t, e, account("2003", 3), "601")
	rec.waitState(t, StateActive, 10*time.Second)
	if c.Codec() != "PCMA" {
		t.Fatalf("2003 only allows alaw, got %s", c.Codec())
	}
	time.Sleep(1500 * time.Millisecond)
	lvl, n := rec.levelDB(800)
	t.Logf("milliwatt level %.1f dBFS over %d samples", lvl, n)
	if lvl < -10 {
		t.Fatalf("tone too quiet: %.1f", lvl)
	}
	c.Hangup()
	rec.waitState(t, StateEnded, 5*time.Second)

	c2, rec2 := dial(t, e, account("2001", 1), "605")
	rec2.waitState(t, StateActive, 10*time.Second)
	rec2.waitState(t, StateEnded, 10*time.Second)
	if info := c2.Info(); info.EndReason != "remote hung up" {
		t.Fatalf("reason %q", info.EndReason)
	}
}

func TestDTMF(t *testing.T) {
	e, _ := newTestEngine(t)
	for _, ext := range []string{"2001", "2004"} { // RFC 4733 and SIP INFO
		a := account(ext, 1)
		if ext == "2004" {
			a.PBX.DTMFMode = "info"
		}
		c, rec := dial(t, e, a, "602")
		rec.waitState(t, StateActive, 10*time.Second)
		time.Sleep(1200 * time.Millisecond)
		if err := c.SendDTMF("4321"); err != nil {
			t.Fatal(err)
		}
		rec.waitState(t, StateEnded, 20*time.Second)
		got := astCLI(t, "database get test dtmf-"+ext)
		if !strings.Contains(got, "4321") {
			t.Fatalf("%s: Asterisk received %q", ext, got)
		}
	}
}

func TestBusyEarlyCancel(t *testing.T) {
	e, _ := newTestEngine(t)
	a := account("2001", 1)

	c, rec := dial(t, e, a, "603")
	rec.waitState(t, StateEnded, 15*time.Second)
	if info := c.Info(); info.EndStatus != "busy" || info.EndCode != 486 {
		t.Fatalf("busy: %+v", info)
	}

	c, rec = dial(t, e, a, "604")
	rec.waitState(t, StateEarly, 10*time.Second)
	time.Sleep(1 * time.Second)
	if lvl, _ := rec.levelDB(0); lvl < -40 {
		t.Fatalf("no early media: %.1f", lvl)
	}
	rec.waitState(t, StateActive, 10*time.Second)
	c.Hangup()
	rec.waitState(t, StateEnded, 5*time.Second)

	c, rec = dial(t, e, a, "699")
	rec.waitState(t, StateRinging, 10*time.Second)
	c.Hangup()
	rec.waitState(t, StateEnded, 10*time.Second)
	if info := c.Info(); info.EndStatus != "cancelled" {
		t.Fatalf("cancel: %+v", info)
	}
	if n := e.ActiveCalls(); n != 0 {
		t.Fatalf("%d calls left", n)
	}
}

func TestHold(t *testing.T) {
	e, _ := newTestEngine(t)
	c, rec := dial(t, e, account("2001", 1), "600")
	rec.waitState(t, StateActive, 10*time.Second)
	if err := c.SetHold(true); err != nil {
		t.Fatal(err)
	}
	if !c.Info().Hold {
		t.Fatal("not on hold")
	}
	if err := c.SetHold(false); err != nil {
		t.Fatal(err)
	}
	c.Hangup()
	rec.waitState(t, StateEnded, 5*time.Second)
}

func TestRegisterAndIncomingCall(t *testing.T) {
	e, h := newTestEngine(t)
	a1, a2 := account("2001", 1), account("2002", 2)
	h.accounts[a2.ContactToken] = a2
	e.SetRegistration(a2, true)
	deadline := time.After(10 * time.Second)
	for e.Registration(2).Status != "registered" {
		select {
		case <-h.regCh:
		case <-deadline:
			t.Fatalf("registration: %+v", e.Registration(2))
		}
	}
	if out := astCLI(t, "pjsip show contacts"); !strings.Contains(out, a2.ContactToken) {
		t.Fatalf("contact not on PBX:\n%s", out)
	}

	// A credential check (REGISTER without Contact) must not touch existing bindings.
	if err := e.CheckCredentials(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if out := astCLI(t, "pjsip show contacts"); !strings.Contains(out, a2.ContactToken) {
		t.Fatalf("credential check removed the registration:\n%s", out)
	}

	calleeRec := newRecorder()
	h.listener = func() CallListener { return calleeRec }
	caller, callerRec := dial(t, e, a1, "2002")
	var in *Call
	select {
	case in = <-h.incoming:
	case <-time.After(10 * time.Second):
		t.Fatal("no incoming call")
	}
	if info := in.Info(); info.Remote != "2001" || info.RemoteName != "Alice" {
		t.Fatalf("caller id %+v", info)
	}
	callerRec.waitState(t, StateRinging, 5*time.Second)
	if err := in.Answer(); err != nil {
		t.Fatal(err)
	}
	callerRec.waitState(t, StateActive, 10*time.Second)
	// caller -> callee audio
	var phase float64
	for i := 0; i < 75; i++ {
		caller.WriteAudio(toneFrame(media.PCMU, &phase))
		time.Sleep(20 * time.Millisecond)
	}
	lvl, n := calleeRec.levelDB(2000)
	t.Logf("callee hears %.1f dBFS (%d samples)", lvl, n)
	if lvl < -30 {
		t.Fatalf("callee hears nothing: %.1f", lvl)
	}
	in.Hangup() // callee hangs up -> caller sees BYE
	callerRec.waitState(t, StateEnded, 10*time.Second)

	// Stop registering: only our binding is removed.
	e.SetRegistration(a2, false)
	time.Sleep(1500 * time.Millisecond)
	if out := astCLI(t, "pjsip show contacts"); strings.Contains(out, a2.ContactToken) {
		t.Fatalf("contact still on PBX after unregister:\n%s", out)
	}
}

func TestWrongPassword(t *testing.T) {
	e, _ := newTestEngine(t)
	a := account("2001", 1)
	a.Password = "wrong"
	if err := e.CheckCredentials(context.Background(), a); err == nil {
		t.Fatal("wrong password accepted")
	}
	a.Password = "Test-2001-pw"
	if err := e.CheckCredentials(context.Background(), a); err != nil {
		t.Fatalf("right password rejected: %v", err)
	}
	c, rec := dial(t, e, &Account{PhoneID: 9, PBX: testPBX(), User: "2001", Password: "nope", ContactToken: "x"}, "600")
	rec.waitState(t, StateEnded, 10*time.Second)
	if info := c.Info(); info.EndStatus != "failed" && info.EndStatus != "rejected" {
		t.Fatalf("%+v", info)
	}
	// Asterisk challenges OPTIONS from unidentified sources; any answer proves reachability.
	if rtt, code, err := e.Ping(context.Background(), &a.PBX); err != nil || code == 0 {
		t.Fatalf("ping: %v %d %v", rtt, code, err)
	}
}
