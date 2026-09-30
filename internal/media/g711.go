// Package media handles the RTP side of calls: G.711 audio, RFC 4733 DTMF and pacing.
package media

// G.711 codecs (ITU-T G.711). The server forwards G.711 payloads unchanged between the
// PBX and clients; these helpers are used for silence, tone generation and tests.

const (
	PCMUSilence byte = 0xFF
	PCMASilence byte = 0xD5
)

// Codec describes a negotiated audio codec.
type Codec struct {
	Name        string // PCMU | PCMA
	PayloadType uint8
	ClockRate   int
}

var (
	PCMU = Codec{Name: "PCMU", PayloadType: 0, ClockRate: 8000}
	PCMA = Codec{Name: "PCMA", PayloadType: 8, ClockRate: 8000}
)

// CodecByName returns a supported codec.
func CodecByName(name string) (Codec, bool) {
	switch name {
	case "PCMU":
		return PCMU, true
	case "PCMA":
		return PCMA, true
	}
	return Codec{}, false
}

func (c Codec) Silence() byte {
	if c.Name == "PCMA" {
		return PCMASilence
	}
	return PCMUSilence
}

func (c Codec) Decode(b byte) int16 {
	if c.Name == "PCMA" {
		return AlawToLinear(b)
	}
	return UlawToLinear(b)
}

func (c Codec) Encode(s int16) byte {
	if c.Name == "PCMA" {
		return LinearToAlaw(s)
	}
	return LinearToUlaw(s)
}

func UlawToLinear(u byte) int16 {
	u = ^u
	t := (int32(u&0x0f) << 3) + 0x84
	t <<= (u & 0x70) >> 4
	if u&0x80 != 0 {
		return int16(0x84 - t)
	}
	return int16(t - 0x84)
}

func LinearToUlaw(s int16) byte {
	const bias, clip = 0x84, 32635
	v := int32(s)
	sign := byte(0)
	if v < 0 {
		v = -v
		sign = 0x80
	}
	if v > clip {
		v = clip
	}
	v += bias
	exp := byte(7)
	for mask := int32(0x4000); v&mask == 0 && exp > 0; mask >>= 1 {
		exp--
	}
	mant := byte((v >> (exp + 3)) & 0x0f)
	return ^(sign | exp<<4 | mant)
}

func AlawToLinear(a byte) int16 {
	a ^= 0x55
	t := int32(a&0x0f) << 4
	seg := (a & 0x70) >> 4
	switch seg {
	case 0:
		t += 8
	case 1:
		t += 0x108
	default:
		t += 0x108
		t <<= seg - 1
	}
	if a&0x80 != 0 {
		return int16(t)
	}
	return int16(-t)
}

func LinearToAlaw(s int16) byte {
	v := int32(s)
	var mask byte
	if v >= 0 {
		mask = 0xD5
	} else {
		mask = 0x55
		v = -v - 1
	}
	if v > 32767 {
		v = 32767
	}
	var seg byte
	switch {
	case v < 256:
		seg = 0
	case v < 512:
		seg = 1
	case v < 1024:
		seg = 2
	case v < 2048:
		seg = 3
	case v < 4096:
		seg = 4
	case v < 8192:
		seg = 5
	case v < 16384:
		seg = 6
	default:
		seg = 7
	}
	var aval byte
	if seg < 2 {
		aval = byte(v>>4) & 0x0f
	} else {
		aval = byte(v>>(seg+3)) & 0x0f
	}
	aval |= seg << 4
	return aval ^ mask
}
