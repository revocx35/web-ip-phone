package sipua

import (
	"strings"
	"testing"

	"github.com/revocx35/web-ip-phone/internal/media"
)

const asteriskOffer = "v=0\r\no=- 1 2 IN IP4 192.168.1.10\r\ns=Asterisk\r\nc=IN IP4 192.168.1.10\r\nt=0 0\r\n" +
	"m=audio 17000 RTP/AVP 8 0 101\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:101 telephone-event/8000\r\n" +
	"a=fmtp:101 0-16\r\na=ptime:20\r\na=sendonly\r\nm=video 0 RTP/AVP 96\r\n"

func TestParseSDP(t *testing.T) {
	r, err := parseSDP([]byte(asteriskOffer))
	if err != nil {
		t.Fatal(err)
	}
	if r.IP.String() != "192.168.1.10" || r.Port != 17000 || r.DTMFPT != 101 || r.Direction != "sendonly" {
		t.Fatalf("%+v", r)
	}
	if len(r.Codecs) != 2 || r.Codecs[0].Name != "PCMA" {
		t.Fatalf("codecs %+v", r.Codecs)
	}
	// our preference wins when answering
	c, ok := pickCodec([]media.Codec{media.PCMU, media.PCMA}, r.Codecs)
	if !ok || c.Name != "PCMU" {
		t.Fatalf("pick %+v", c)
	}
	if _, ok := pickCodec([]media.Codec{media.PCMU}, []media.Codec{{Name: "G722", PayloadType: 9}}); ok {
		t.Fatal("unsupported codec picked")
	}
}

func TestParseSDPRejects(t *testing.T) {
	for _, bad := range []string{"", "v=0\r\n", "v=0\r\nm=audio 99999 RTP/AVP 0\r\n", "v=0\r\nm=audio 4000 RTP/AVP 0\r\n", strings.Repeat("a", 20000)} {
		if _, err := parseSDP([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad[:min(len(bad), 40)])
		}
	}
}

func TestLocalSDPRoundTrip(t *testing.T) {
	l := localSDP{SessionID: 1, Version: 1, IP: []byte{10, 0, 0, 5}, Port: 20000, Codecs: []media.Codec{media.PCMU, media.PCMA}, DTMFPT: 101, Direction: "sendonly"}
	r, err := parseSDP(l.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if r.Port != 20000 || r.IP.String() != "10.0.0.5" || r.DTMFPT != 101 || r.Direction != "sendonly" || len(r.Codecs) != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestValidators(t *testing.T) {
	for s, ok := range map[string]bool{"1001": true, "alice.b": true, "a b": false, "1001\r\nVia: x": false, "": false, "x@y": false} {
		if ValidUser(s) != ok {
			t.Errorf("ValidUser(%q)", s)
		}
	}
	for s, ok := range map[string]bool{"*43": true, "+4930123": true, "#12": true, "12;34": false, "1 2": false, "<sip:x>": false} {
		if ValidDialString(s) != ok {
			t.Errorf("ValidDialString(%q)", s)
		}
	}
	for s, ok := range map[string]bool{"Front Desk": true, "Çağrı": true, `Evil "Name`: false, "a\\b": false, "x\ny": false} {
		if ValidDisplayName(s) != ok {
			t.Errorf("ValidDisplayName(%q)", s)
		}
	}
	for s, ok := range map[string]bool{"pbx.local": true, "192.168.1.1": true, "::1": true, "-bad.com": false, "a..b": false, "evil.com/x": false} {
		if ValidHost(s) != ok {
			t.Errorf("ValidHost(%q)", s)
		}
	}
	if escapeUser("12#") != "12%23" || unescapeUser("12%23") != "12#" {
		t.Error("escape")
	}
	if cleanRemoteText("Bob\x00\x1b[31m") != "Bob[31m" {
		t.Errorf("clean: %q", cleanRemoteText("Bob\x00\x1b[31m"))
	}
}

func TestRequestURI(t *testing.T) {
	p := PBX{Host: "10.0.0.1", Port: 5160, Transport: "udp"}
	u, route := p.requestURI("600")
	if u.String() != "sip:600@10.0.0.1:5160" || route != nil {
		t.Fatalf("%s %v", u.String(), route)
	}
	p = PBX{Host: "10.0.0.1", Port: 5060, Transport: "udp", Domain: "pbx.example.com"}
	u, route = p.requestURI("600")
	if u.String() != "sip:600@pbx.example.com" || route == nil || !strings.Contains(route.Value(), "10.0.0.1:5060;lr") {
		t.Fatalf("%s %v", u.String(), route)
	}
}
