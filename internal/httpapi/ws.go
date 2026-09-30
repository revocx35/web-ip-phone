package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/phone"
	"github.com/revocx35/web-ip-phone/internal/store"
)

const (
	wsPingInterval   = 20 * time.Second
	wsSessionRecheck = time.Minute
	wsQueue          = 256
)

// wsConn implements phone.Outbound.
type wsConn struct {
	c      *websocket.Conn
	out    chan wsMsg
	done   chan struct{}
	once   sync.Once
	reason string
	mu     sync.Mutex
}

type wsMsg struct {
	binary bool
	data   []byte
}

func (w *wsConn) SendJSON(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	select {
	case w.out <- wsMsg{data: b}:
		return true
	case <-w.done:
		return false
	default:
		// A client that cannot keep up with control messages is broken or malicious.
		w.Close("connection too slow")
		return false
	}
}

func (w *wsConn) SendBinary(b []byte) bool {
	select {
	case w.out <- wsMsg{binary: true, data: b}:
		return true
	default:
		return false // drop audio rather than build up latency
	}
}

func (w *wsConn) Close(reason string) {
	w.once.Do(func() {
		w.mu.Lock()
		w.reason = reason
		w.mu.Unlock()
		close(w.done)
	})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) error {
	a := authOf(r)
	if !s.wsLimiter.Allow(clientIP(r)) {
		return &apiError{status: http.StatusTooManyRequests, code: "rate_limited", msg: "too many connections"}
	}
	// Browsers authenticate with the cookie, so the Origin must be ours (cross-site
	// WebSocket hijacking). The app uses a bearer token and sends no Origin.
	if !a.Bearer {
		if o := r.Header.Get("Origin"); o == "" || !s.originAllowed(r, o) {
			return &apiError{status: http.StatusForbidden, code: "csrf", msg: "cross-origin WebSocket refused"}
		}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // origin checked above
	if err != nil {
		return nil // Accept wrote the error response
	}
	c.SetReadLimit(64 << 10)
	conn := &wsConn{c: c, out: make(chan wsMsg, wsQueue), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	user := s.meViewOf(r.Context(), a.User, a.Settings, a.Session.ID)
	client := s.svc.Connect(ctx, a.User.ID, a.User.Username, a.Session.ID, user, conn)
	defer s.svc.Disconnect(client)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // writer
		defer wg.Done()
		defer cancel()
		ping := time.NewTicker(wsPingInterval)
		defer ping.Stop()
		recheck := time.NewTicker(wsSessionRecheck)
		defer recheck.Stop()
		for {
			select {
			case m := <-conn.out:
				typ := websocket.MessageText
				if m.binary {
					typ = websocket.MessageBinary
				}
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := c.Write(wctx, typ, m.data)
				wcancel()
				if err != nil {
					return
				}
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
				err := c.Ping(pctx)
				pcancel()
				if err != nil {
					return
				}
			case <-recheck.C:
				if !s.sessionAlive(ctx, a.Session.ID, s.requestTokenHash(r)) {
					conn.Close("session ended")
				}
			case <-conn.done:
				conn.mu.Lock()
				reason := conn.reason
				conn.mu.Unlock()
				if len(reason) > 100 {
					reason = reason[:100]
				}
				c.Close(websocket.StatusPolicyViolation, reason)
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { // reader
		defer wg.Done()
		defer cancel()
		ctl := auth.NewRateLimiter(30*60, 60)   // 30 control messages/s
		audio := auth.NewRateLimiter(120*60, 200) // 20 ms frames need 50/s
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if audio.Allow("") {
					s.svc.Audio(client, data)
				}
				continue
			}
			if !ctl.Allow("") {
				conn.Close("too many messages")
				return
			}
			var m phone.Inbound
			if err := json.Unmarshal(data, &m); err != nil {
				conn.SendJSON(map[string]string{"type": "error", "code": "invalid", "error": "invalid message"})
				continue
			}
			s.svc.Handle(ctx, client, &m)
		}
	}()
	wg.Wait()
	c.CloseNow()
	return nil
}

// sessionAlive re-checks that the connection's session was not revoked or expired.
func (s *Server) sessionAlive(ctx context.Context, sessionID int64, tokenHash []byte) bool {
	if tokenHash == nil {
		return false
	}
	se, err := s.store.SessionByToken(ctx, tokenHash)
	if errors.Is(err, store.ErrNotFound) || (err == nil && se.ID != sessionID) {
		return false
	}
	if err != nil {
		return true // database hiccup: don't drop calls for it
	}
	u, err := s.store.GetUser(ctx, se.UserID)
	return err == nil && !u.Disabled
}

// requestTokenHash returns the hash of the session token a request authenticated with.
func (s *Server) requestTokenHash(r *http.Request) []byte {
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		return auth.HashToken(h[7:])
	}
	if c, err := r.Cookie(cookieName(client(r).HTTPS)); err == nil {
		return auth.HashToken(c.Value)
	}
	return nil
}
