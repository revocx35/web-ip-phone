package sipua

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/revocx35/web-ip-phone/internal/media"
)

const dtmfPayloadType = 101

// localSDP describes our side of the media session.
type localSDP struct {
	SessionID uint64
	Version   uint64
	IP        net.IP
	Port      int
	Codecs    []media.Codec // preference order
	DTMFPT    uint8         // telephone-event payload type, 0 = none
	Direction string        // sendrecv | sendonly | recvonly | inactive
}

func (l *localSDP) Marshal() []byte {
	ipver := "IP4"
	if l.IP.To4() == nil {
		ipver = "IP6"
	}
	var pts []string
	for _, c := range l.Codecs {
		pts = append(pts, strconv.Itoa(int(c.PayloadType)))
	}
	if l.DTMFPT != 0 {
		pts = append(pts, strconv.Itoa(int(l.DTMFPT)))
	}
	dir := l.Direction
	if dir == "" {
		dir = "sendrecv"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "v=0\r\n")
	fmt.Fprintf(&b, "o=- %d %d IN %s %s\r\n", l.SessionID, l.Version, ipver, l.IP)
	fmt.Fprintf(&b, "s=WebIPPhone\r\n")
	fmt.Fprintf(&b, "c=IN %s %s\r\n", ipver, l.IP)
	fmt.Fprintf(&b, "t=0 0\r\n")
	fmt.Fprintf(&b, "m=audio %d RTP/AVP %s\r\n", l.Port, strings.Join(pts, " "))
	for _, c := range l.Codecs {
		fmt.Fprintf(&b, "a=rtpmap:%d %s/%d\r\n", c.PayloadType, c.Name, c.ClockRate)
	}
	if l.DTMFPT != 0 {
		fmt.Fprintf(&b, "a=rtpmap:%d telephone-event/8000\r\n", l.DTMFPT)
		fmt.Fprintf(&b, "a=fmtp:%d 0-16\r\n", l.DTMFPT)
	}
	fmt.Fprintf(&b, "a=ptime:20\r\n")
	fmt.Fprintf(&b, "a=%s\r\n", dir)
	return []byte(b.String())
}

// remoteSDP is the relevant part of the peer's session description.
type remoteSDP struct {
	IP        net.IP
	Port      int
	Codecs    []media.Codec // supported codecs, in the peer's order
	DTMFPT    uint8         // 0 = none
	Direction string
}

var errNoAudio = errors.New("no usable audio stream in SDP")

// parseSDP extracts the first audio stream. Only G.711 codecs are considered.
func parseSDP(body []byte) (*remoteSDP, error) {
	if len(body) == 0 || len(body) > 16*1024 {
		return nil, errNoAudio
	}
	var (
		sessIP, mediaIP net.IP
		inAudio, done   bool
		port            int
		pts             []int
		rtpmap          = map[int]string{}
		sessDir, medDir string
	)
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimRight(raw, "\r")
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		k, v := line[0], line[2:]
		switch k {
		case 'm':
			if inAudio {
				done = true // only the first audio stream
			}
			if done {
				inAudio = false
				continue
			}
			f := strings.Fields(v)
			inAudio = len(f) >= 4 && f[0] == "audio" && strings.HasPrefix(f[2], "RTP/")
			if !inAudio {
				continue
			}
			p, err := strconv.Atoi(f[1])
			if err != nil || p < 0 || p > 65535 {
				return nil, fmt.Errorf("bad media port %q", f[1])
			}
			port = p
			for _, s := range f[3:] {
				if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < 128 {
					pts = append(pts, n)
				}
			}
		case 'c':
			f := strings.Fields(v)
			if len(f) < 3 || f[0] != "IN" {
				continue
			}
			addr := f[2]
			if i := strings.IndexByte(addr, '/'); i >= 0 {
				addr = addr[:i]
			}
			ip := net.ParseIP(addr)
			if ip == nil {
				continue
			}
			if inAudio {
				mediaIP = ip
			} else if port == 0 && !done {
				sessIP = ip
			}
		case 'a':
			switch {
			case strings.HasPrefix(v, "rtpmap:") && inAudio:
				f := strings.Fields(v[len("rtpmap:"):])
				if len(f) == 2 {
					if n, err := strconv.Atoi(f[0]); err == nil {
						rtpmap[n] = strings.ToLower(f[1])
					}
				}
			case v == "sendrecv" || v == "sendonly" || v == "recvonly" || v == "inactive":
				if inAudio {
					medDir = v
				} else if port == 0 {
					sessDir = v
				}
			}
		}
	}
	if port == 0 && len(pts) == 0 {
		return nil, errNoAudio
	}
	r := &remoteSDP{Port: port, IP: mediaIP, Direction: medDir}
	if r.IP == nil {
		r.IP = sessIP
	}
	if r.Direction == "" {
		r.Direction = sessDir
	}
	if r.Direction == "" {
		r.Direction = "sendrecv"
	}
	for _, pt := range pts {
		name := rtpmap[pt]
		switch {
		case pt == 0 && (name == "" || strings.HasPrefix(name, "pcmu/8000")):
			r.Codecs = append(r.Codecs, media.PCMU)
		case pt == 8 && (name == "" || strings.HasPrefix(name, "pcma/8000")):
			r.Codecs = append(r.Codecs, media.PCMA)
		case strings.HasPrefix(name, "pcmu/8000"):
			r.Codecs = append(r.Codecs, media.Codec{Name: "PCMU", PayloadType: uint8(pt), ClockRate: 8000})
		case strings.HasPrefix(name, "pcma/8000"):
			r.Codecs = append(r.Codecs, media.Codec{Name: "PCMA", PayloadType: uint8(pt), ClockRate: 8000})
		case strings.HasPrefix(name, "telephone-event/8000") && r.DTMFPT == 0 && pt != 0:
			r.DTMFPT = uint8(pt)
		}
	}
	if r.IP == nil && port != 0 {
		return nil, errors.New("SDP has no connection address")
	}
	return r, nil
}

// pickCodec returns the first codec in order that both sides support.
func pickCodec(order []media.Codec, other []media.Codec) (media.Codec, bool) {
	for _, a := range order {
		for _, b := range other {
			if a.Name == b.Name {
				return b, true // use the peer's payload type
			}
		}
	}
	return media.Codec{}, false
}

// codecsFromNames maps a PBX codec preference list to codecs.
func codecsFromNames(names []string) []media.Codec {
	var out []media.Codec
	for _, n := range names {
		if c, ok := media.CodecByName(strings.TrimSpace(n)); ok {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []media.Codec{media.PCMU, media.PCMA}
	}
	return out
}
