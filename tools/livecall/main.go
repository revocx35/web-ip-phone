// Command livecall places a test call through a running Web IP Phone server, the way the
// Android app does (bearer token + WebSocket), and checks that audio flows both ways.
//
//	go run ./tools/livecall -url https://127.0.0.1:8443 -insecure -user admin -phone 1 -number '*43'
//
// It sends an 800 Hz tone and reports how strongly that tone comes back (echo tests such
// as FreePBX *43 return it), plus the overall level received. The password is read from
// WEBPHONE_PASSWORD or prompted on stdin.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/revocx35/web-ip-phone/internal/media"
)

func main() {
	url := flag.String("url", "https://127.0.0.1:8443", "server URL")
	insecure := flag.Bool("insecure", false, "accept a self-signed certificate")
	user := flag.String("user", "admin", "username")
	phoneID := flag.Int64("phone", 0, "phone ID (0 = first usable phone)")
	number := flag.String("number", "*43", "number to call")
	seconds := flag.Int("seconds", 8, "call duration")
	totp := flag.String("totp", "", "2FA code, if enabled")
	flag.Parse()

	pw := os.Getenv("WEBPHONE_PASSWORD")
	if pw == "" {
		fmt.Fprint(os.Stderr, "password: ")
		pw, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimSpace(pw)
	}
	hc := &http.Client{Timeout: 15 * time.Second}
	if *insecure {
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // test tool
	}
	post := func(path string, body any, out any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", *url+"/api/v1"+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "webphone") // the server's login-CSRF check
		res, err := hc.Do(req)
		must(err)
		defer res.Body.Close()
		if res.StatusCode != 200 {
			var e map[string]string
			json.NewDecoder(res.Body).Decode(&e)
			fail("%s: %d %s", path, res.StatusCode, e["error"])
		}
		must(json.NewDecoder(res.Body).Decode(out))
	}
	var login struct {
		Token       string `json:"token"`
		MFARequired bool   `json:"mfaRequired"`
		Ticket      string `json:"ticket"`
	}
	post("/auth/login", map[string]string{"username": *user, "password": pw, "client": "app", "deviceName": "livecall"}, &login)
	if login.MFARequired {
		if *totp == "" {
			fail("2FA is on: pass -totp")
		}
		post("/auth/mfa", map[string]string{"ticket": login.Ticket, "code": *totp}, &login)
	}
	defer func() {
		req, _ := http.NewRequest("POST", *url+"/api/v1/auth/logout", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+login.Token)
		req.Header.Set("Content-Type", "application/json")
		if res, err := hc.Do(req); err == nil {
			res.Body.Close()
		}
	}()

	ctx := context.Background()
	wsURL := "wss" + strings.TrimPrefix(*url, "https") + "/api/v1/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: hc,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + login.Token}}})
	must(err)
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	msgs := make(chan map[string]any, 64)
	audio := make(chan []byte, 2000)
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				close(msgs)
				return
			}
			if typ == websocket.MessageBinary {
				select {
				case audio <- data:
				default:
				}
				continue
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			msgs <- m
		}
	}()
	send := func(v any) {
		b, _ := json.Marshal(v)
		must(conn.Write(ctx, websocket.MessageText, b))
	}
	wait := func(what string, d time.Duration, f func(map[string]any) bool) map[string]any {
		t := time.After(d)
		for {
			select {
			case m, ok := <-msgs:
				if !ok {
					fail("connection closed while waiting for %s", what)
				}
				if m["type"] == "error" {
					fail("server: %v", m["error"])
				}
				if f(m) {
					return m
				}
			case <-t:
				fail("timeout waiting for %s", what)
			}
		}
	}

	hello := wait("hello", 5*time.Second, func(m map[string]any) bool { return m["type"] == "hello" })
	phones, _ := hello["phones"].([]any)
	if len(phones) == 0 {
		fail("this user has no usable phone")
	}
	if *phoneID == 0 {
		*phoneID = int64(phones[0].(map[string]any)["id"].(float64))
	}
	fmt.Printf("dialing %s from phone %d\n", *number, *phoneID)
	send(map[string]any{"type": "dial", "phone": *phoneID, "number": *number, "req": "1"})
	ack := wait("dial ack", 10*time.Second, func(m map[string]any) bool { return m["type"] == "ack" })
	callID := ack["call"].(string)
	var codec media.Codec
	wait("answer", 30*time.Second, func(m map[string]any) bool {
		c, _ := m["call"].(map[string]any)
		if c == nil || c["id"] != callID {
			return false
		}
		fmt.Printf("  state: %v %v\n", c["state"], c["endReason"])
		if c["state"] == "ended" {
			fail("call ended: %v", c["endReason"])
		}
		if c["state"] == "active" {
			codec, _ = media.CodecByName(c["codec"].(string))
			return true
		}
		return false
	})
	fmt.Printf("answered, codec %s\n", codec.Name)

	// Send the tone paced at 20 ms and collect what comes back.
	var rx []float64
	tick := time.NewTicker(20 * time.Millisecond)
	phase := 0.0
	end := time.After(time.Duration(*seconds) * time.Second)
loop:
	for {
		select {
		case <-tick.C:
			f := make([]byte, 161)
			f[0] = 1
			for i := 1; i < len(f); i++ {
				f[i] = codec.Encode(int16(6000 * math.Sin(phase)))
				phase += 2 * math.Pi * 800 / 8000
			}
			conn.Write(ctx, websocket.MessageBinary, f)
		case a := <-audio:
			for _, b := range a[1:] {
				rx = append(rx, float64(codec.Decode(b)))
			}
		case <-end:
			break loop
		}
	}
	tick.Stop()
	send(map[string]any{"type": "hangup", "call": callID})
	time.Sleep(500 * time.Millisecond)

	fmt.Printf("received %.1f s of audio\n", float64(len(rx))/8000)
	// Analyse per 100 ms block: overall level and the 800 Hz share (Goertzel).
	strong := 0
	blocks := 0
	for i := 0; i+800 <= len(rx); i += 800 {
		blk := rx[i : i+800]
		var sum float64
		for _, v := range blk {
			sum += v * v
		}
		rms := math.Sqrt(sum / 800)
		p := goertzel(blk, 800)
		ratio := p / (sum + 1)
		blocks++
		if ratio > 0.5 && rms > 300 {
			strong++
		}
	}
	fmt.Printf("blocks with our 800 Hz tone coming back: %d of %d (100 ms each)\n", strong, blocks)
	fmt.Print("per second (level dBFS / 800 Hz share %):")
	for i := 0; i+8000 <= len(rx); i += 8000 {
		blk := rx[i : i+8000]
		var sum float64
		for _, v := range blk {
			sum += v * v
		}
		db := 10 * math.Log10(sum/8000/(32768*32768)+1e-12)
		fmt.Printf(" %d:%.0f/%.0f", i/8000, db, 100*goertzel(blk, 800)/(sum+1))
	}
	fmt.Println()
	if len(rx) < 8000 {
		fail("almost no audio received")
	}
	if strong == 0 {
		fmt.Println("no echo of the test tone detected (fine unless the number is an echo test)")
	}
}

// goertzel returns the power of freq in the block, normalised to be comparable with sum(x^2).
func goertzel(x []float64, freq float64) float64 {
	k := 2 * math.Cos(2*math.Pi*freq/8000)
	var s1, s2 float64
	for _, v := range x {
		s := v + k*s1 - s2
		s2, s1 = s1, s
	}
	p := s1*s1 + s2*s2 - k*s1*s2
	return 2 * p / float64(len(x))
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "livecall: "+format+"\n", a...)
	os.Exit(1)
}
