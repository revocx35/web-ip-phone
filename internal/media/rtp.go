package media

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	FrameSamples  = 160 // 20 ms at 8 kHz
	FrameDuration = 20 * time.Millisecond
	maxQueue      = 25 // frames buffered from the client (500 ms)
	targetQueue   = 3  // after a burst, drop down to this many frames (60 ms)
	burstQueue    = 8  // start dropping above this (160 ms)
)

// PortPool hands out even UDP ports from a range.
type PortPool struct {
	mu       sync.Mutex
	min, max int
	next     int
	used     map[int]bool
	bindIP   net.IP
}

func NewPortPool(min, max int, bindIP net.IP) *PortPool {
	if min%2 == 1 {
		min++
	}
	return &PortPool{min: min, max: max, next: min, used: map[int]bool{}, bindIP: bindIP}
}

// Listen binds the next free even port.
func (p *PortPool) Listen() (*net.UDPConn, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	span := (p.max - p.min) / 2
	for i := 0; i <= span; i++ {
		port := p.next
		p.next += 2
		if p.next > p.max {
			p.next = p.min
		}
		if p.used[port] {
			continue
		}
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: p.bindIP, Port: port})
		if err != nil {
			continue
		}
		p.used[port] = true
		return c, port, nil
	}
	return nil, 0, errors.New("no free RTP port")
}

func (p *PortPool) Release(port int) {
	p.mu.Lock()
	delete(p.used, port)
	p.mu.Unlock()
}

// Session is one RTP audio stream with the PBX.
type Session struct {
	conn  *net.UDPConn
	port  int
	pool  *PortPool
	codec atomic.Pointer[Codec]

	dtmfPT atomic.Uint32 // 0 = RFC 4733 not negotiated

	mu        sync.Mutex
	remote    *net.UDPAddr // where we send
	allowedIP net.IP       // packets from other IPs are dropped
	latched   bool

	ssrc uint32
	seq  uint16
	ts   uint32

	queueMu sync.Mutex
	queue   [][]byte
	dtmf    []byte // digits waiting to be sent
	hold    atomic.Bool

	// OnAudio receives each audio payload from the PBX (called from the read goroutine).
	OnAudio func(payload []byte)
	// OnDTMF receives digits sent by the PBX with RFC 4733.
	OnDTMF func(digit byte)

	RxPackets, TxPackets, RxDropped atomic.Uint64
	lastRx                          atomic.Int64

	closeOnce sync.Once
	done      chan struct{}
	started   atomic.Bool
}

// NewSession binds a local RTP port.
func NewSession(pool *PortPool) (*Session, error) {
	c, port, err := pool.Listen()
	if err != nil {
		return nil, err
	}
	var b [10]byte
	rand.Read(b[:])
	s := &Session{
		conn: c, port: port, pool: pool, done: make(chan struct{}),
		ssrc: binary.BigEndian.Uint32(b[0:4]),
		seq:  binary.BigEndian.Uint16(b[4:6]),
		ts:   binary.BigEndian.Uint32(b[6:10]),
	}
	return s, nil
}

func (s *Session) LocalPort() int { return s.port }

// SetRemote sets the destination from the remote SDP. allowedIP is the source address
// accepted for incoming packets (the SDP connection address).
func (s *Session) SetRemote(addr *net.UDPAddr, codec Codec, dtmfPT uint8) {
	s.mu.Lock()
	s.remote = addr
	s.allowedIP = addr.IP
	s.latched = false
	s.mu.Unlock()
	c := codec
	s.codec.Store(&c)
	s.dtmfPT.Store(uint32(dtmfPT))
}

func (s *Session) Codec() Codec {
	if c := s.codec.Load(); c != nil {
		return *c
	}
	return PCMU
}

// Start begins receiving and the 20 ms send clock. Safe to call more than once.
func (s *Session) Start() {
	if s.started.Swap(true) {
		return
	}
	go s.readLoop()
	go s.sendLoop()
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.conn.Close()
		s.pool.Release(s.port)
	})
}

// SetHold stops sending audio (silence is still sent to keep NAT/RTP timers alive).
func (s *Session) SetHold(on bool) { s.hold.Store(on) }

// WriteAudio queues G.711 audio from the client, split into 20 ms frames.
func (s *Session) WriteAudio(b []byte) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	for len(b) > 0 {
		n := min(len(b), FrameSamples)
		f := make([]byte, FrameSamples)
		copy(f, b[:n])
		if n < FrameSamples {
			fill := s.Codec().Silence()
			for i := n; i < FrameSamples; i++ {
				f[i] = fill
			}
		}
		b = b[n:]
		if len(s.queue) >= maxQueue {
			s.queue = s.queue[1:]
		}
		s.queue = append(s.queue, f)
	}
}

// SendDTMF queues digits (0-9 * # A-D) for RFC 4733 transmission.
func (s *Session) SendDTMF(digits string) error {
	if s.dtmfPT.Load() == 0 {
		return errors.New("RFC 4733 telephone-event not negotiated")
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	for i := 0; i < len(digits); i++ {
		if _, ok := dtmfEvent(digits[i]); !ok {
			return fmt.Errorf("invalid DTMF digit %q", digits[i])
		}
	}
	if len(s.dtmf)+len(digits) > 64 {
		return errors.New("too many queued DTMF digits")
	}
	s.dtmf = append(s.dtmf, digits...)
	return nil
}

func dtmfEvent(d byte) (byte, bool) {
	switch {
	case d >= '0' && d <= '9':
		return d - '0', true
	case d == '*':
		return 10, true
	case d == '#':
		return 11, true
	case d >= 'A' && d <= 'D':
		return 12 + d - 'A', true
	case d >= 'a' && d <= 'd':
		return 12 + d - 'a', true
	}
	return 0, false
}

func eventDigit(e byte) byte {
	switch {
	case e <= 9:
		return '0' + e
	case e == 10:
		return '*'
	case e == 11:
		return '#'
	case e <= 15:
		return 'A' + e - 12
	}
	return 0
}

// LastRx is the time of the last packet from the PBX (zero if none).
func (s *Session) LastRx() time.Time {
	v := s.lastRx.Load()
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}

func (s *Session) readLoop() {
	buf := make([]byte, 1500)
	var lastEventTS uint32
	var haveEvent bool
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		s.mu.Lock()
		allowed := s.allowedIP != nil && from.IP.Equal(s.allowedIP)
		if allowed && !s.latched && s.remote != nil && (s.remote.Port != from.Port) {
			// Symmetric RTP: the PBX sends from a different port than it advertised
			// (NAT); answer to where the packets really come from.
			s.remote = &net.UDPAddr{IP: from.IP, Port: from.Port}
		}
		if allowed {
			s.latched = true
		}
		s.mu.Unlock()
		if !allowed {
			s.RxDropped.Add(1)
			continue
		}
		pkt := buf[:n]
		payload, pt, _, ts, ok := parseRTP(pkt)
		if !ok {
			s.RxDropped.Add(1)
			continue
		}
		s.RxPackets.Add(1)
		s.lastRx.Store(time.Now().UnixNano())
		codec := s.Codec()
		switch {
		case pt == codec.PayloadType:
			if s.OnAudio != nil && len(payload) > 0 {
				s.OnAudio(payload)
			}
		case uint32(pt) == s.dtmfPT.Load() && pt != 0:
			// All packets of one event share its start timestamp: report each event once.
			if len(payload) >= 4 && s.OnDTMF != nil && (!haveEvent || ts != lastEventTS) {
				haveEvent, lastEventTS = true, ts
				if d := eventDigit(payload[0]); d != 0 {
					s.OnDTMF(d)
				}
			}
		}
	}
}

// parseRTP validates an RTP packet and returns its payload.
func parseRTP(b []byte) (payload []byte, pt uint8, marker bool, ts uint32, ok bool) {
	if len(b) < 12 || b[0]>>6 != 2 {
		return nil, 0, false, 0, false
	}
	cc := int(b[0] & 0x0f)
	hasExt := b[0]&0x10 != 0
	hasPad := b[0]&0x20 != 0
	marker = b[1]&0x80 != 0
	pt = b[1] & 0x7f
	ts = binary.BigEndian.Uint32(b[4:8])
	off := 12 + 4*cc
	if len(b) < off {
		return nil, 0, false, 0, false
	}
	if hasExt {
		if len(b) < off+4 {
			return nil, 0, false, 0, false
		}
		off += 4 + 4*int(binary.BigEndian.Uint16(b[off+2:off+4]))
		if len(b) < off {
			return nil, 0, false, 0, false
		}
	}
	end := len(b)
	if hasPad {
		p := int(b[end-1])
		if p == 0 || end-p < off {
			return nil, 0, false, 0, false
		}
		end -= p
	}
	return b[off:end], pt, marker, ts, true
}

func (s *Session) sendPacket(pt uint8, marker bool, ts uint32, payload []byte) {
	s.mu.Lock()
	remote := s.remote
	s.mu.Unlock()
	if remote == nil {
		return
	}
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80
	pkt[1] = pt
	if marker {
		pkt[1] |= 0x80
	}
	binary.BigEndian.PutUint16(pkt[2:4], s.seq)
	binary.BigEndian.PutUint32(pkt[4:8], ts)
	binary.BigEndian.PutUint32(pkt[8:12], s.ssrc)
	copy(pkt[12:], payload)
	s.seq++
	if _, err := s.conn.WriteToUDP(pkt, remote); err == nil {
		s.TxPackets.Add(1)
	}
}

// sendLoop emits one packet every 20 ms: client audio, silence, or DTMF events.
func (s *Session) sendLoop() {
	ticker := time.NewTicker(FrameDuration)
	defer ticker.Stop()
	first := true

	// DTMF state (RFC 4733): each digit is sent for 100 ms, then 3 end packets, then 60 ms gap.
	const (
		toneFrames = 5
		gapFrames  = 3
	)
	var (
		inEvent   bool
		event     byte
		eventTS   uint32
		eventTick int
		gap       int
	)
	silence := make([]byte, FrameSamples)
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
		}
		codec := s.Codec()

		s.queueMu.Lock()
		if len(s.queue) > burstQueue {
			s.queue = s.queue[len(s.queue)-targetQueue:]
		}
		var frame []byte
		if len(s.queue) > 0 {
			frame = s.queue[0]
			s.queue = s.queue[1:]
		}
		if !inEvent && gap == 0 && len(s.dtmf) > 0 {
			event, _ = dtmfEvent(s.dtmf[0])
			s.dtmf = s.dtmf[1:]
			inEvent, eventTS, eventTick = true, s.ts, 0
		}
		s.queueMu.Unlock()

		if inEvent {
			eventTick++
			dur := uint16(eventTick * FrameSamples)
			pt := uint8(s.dtmfPT.Load())
			if eventTick <= toneFrames {
				s.sendPacket(pt, eventTick == 1, eventTS, []byte{event, 10, byte(dur >> 8), byte(dur)})
			} else {
				// End of event, sent three times for robustness (RFC 4733 §2.5.1.4).
				dur = uint16(toneFrames * FrameSamples)
				for i := 0; i < 3; i++ {
					s.sendPacket(pt, false, eventTS, []byte{event, 0x80 | 10, byte(dur >> 8), byte(dur)})
				}
				inEvent, gap = false, gapFrames
			}
			s.ts += FrameSamples
			continue
		}
		if gap > 0 {
			gap--
		}

		if frame == nil || s.hold.Load() {
			for i := range silence {
				silence[i] = codec.Silence()
			}
			frame = silence
		}
		s.sendPacket(codec.PayloadType, first, s.ts, frame)
		first = false
		s.ts += FrameSamples
	}
}
