package media

import (
	"encoding/binary"
	"testing"
)

func TestG711RoundTrip(t *testing.T) {
	for _, c := range []Codec{PCMU, PCMA} {
		for v := -32768; v <= 32767; v += 97 {
			got := c.Decode(c.Encode(int16(v)))
			diff := int(got) - v
			if diff < 0 {
				diff = -diff
			}
			// G.711 quantisation error grows with amplitude (~1/16 of the value).
			limit := 16 + abs(v)/12
			if diff > limit {
				t.Fatalf("%s: %d -> %d", c.Name, v, got)
			}
		}
		if s := c.Decode(c.Silence()); s > 16 || s < -16 {
			t.Fatalf("%s silence decodes to %d", c.Name, s)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestParseRTP(t *testing.T) {
	pkt := make([]byte, 12+4+8+160+3)
	pkt[0] = 0x80 | 0x20 | 0x10 | 1 // V=2, padding, extension, 1 CSRC
	pkt[1] = 0x80 | 8               // marker, PCMA
	binary.BigEndian.PutUint32(pkt[4:], 12345)
	off := 12 + 4
	binary.BigEndian.PutUint16(pkt[off+2:], 1) // extension length 1 word
	off += 8
	for i := 0; i < 160; i++ {
		pkt[off+i] = 0x55
	}
	pkt[len(pkt)-1] = 3
	payload, pt, marker, ts, ok := parseRTP(pkt)
	if !ok || pt != 8 || !marker || ts != 12345 || len(payload) != 160 || payload[0] != 0x55 {
		t.Fatalf("%v %d %v %d %d", ok, pt, marker, ts, len(payload))
	}
	for _, bad := range [][]byte{nil, make([]byte, 11), {0x40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, {0x8f, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}} {
		if _, _, _, _, ok := parseRTP(bad); ok {
			t.Errorf("accepted %x", bad)
		}
	}
}

func TestDTMFEvents(t *testing.T) {
	for _, d := range []byte("0123456789*#ABCD") {
		e, ok := dtmfEvent(d)
		if !ok || eventDigit(e) != d {
			t.Errorf("%c", d)
		}
	}
	if _, ok := dtmfEvent('x'); ok {
		t.Error("x accepted")
	}
}
