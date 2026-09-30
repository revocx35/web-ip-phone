package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	SessionWeb = "web" // browser, cookie
	SessionApp = "app" // native client, bearer token
)

type Session struct {
	ID          int64         `json:"id"`
	UserID      int64         `json:"-"`
	Kind        string        `json:"kind"`
	Name        string        `json:"name"`
	CreatedAt   time.Time     `json:"createdAt"`
	LastSeenAt  time.Time     `json:"lastSeenAt"`
	ExpiresAt   time.Time     `json:"expiresAt"`
	IdleTimeout time.Duration `json:"-"`
	CreatedIP   string        `json:"createdIp"`
	LastIP      string        `json:"lastIp"`
}

const sessionCols = "id, user_id, kind, name, created_at, last_seen_at, expires_at, idle_seconds, created_ip, last_ip"

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var se Session
	var ca, ls, ex, idle int64
	err := row.Scan(&se.ID, &se.UserID, &se.Kind, &se.Name, &ca, &ls, &ex, &idle, &se.CreatedIP, &se.LastIP)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	se.CreatedAt, se.LastSeenAt, se.ExpiresAt = unix(ca), unix(ls), unix(ex)
	se.IdleTimeout = time.Duration(idle) * time.Second
	return &se, nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, kind, name, ip string, idle, maxAge time.Duration) (*Session, error) {
	now := s.nowUnix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, kind, name, created_at, last_seen_at,
		expires_at, idle_seconds, created_ip, last_ip) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		tokenHash, userID, kind, name, now, now, now+int64(maxAge/time.Second), int64(idle/time.Second), ip, ip)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return scanSession(s.db.QueryRowContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE id = ?", id))
}

// SessionByToken returns a live session. Expired sessions are deleted and reported as not found.
func (s *Store) SessionByToken(ctx context.Context, tokenHash []byte) (*Session, error) {
	se, err := scanSession(s.db.QueryRowContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE token_hash = ?", tokenHash))
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !now.Before(se.ExpiresAt) || !now.Before(se.LastSeenAt.Add(se.IdleTimeout)) {
		s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", se.ID)
		return nil, ErrNotFound
	}
	return se, nil
}

// TouchSession records activity (at most once a minute per session to limit writes).
func (s *Store) TouchSession(ctx context.Context, se *Session, ip string) error {
	now := s.now()
	if now.Sub(se.LastSeenAt) < time.Minute && ip == se.LastIP {
		return nil
	}
	se.LastSeenAt, se.LastIP = now.UTC().Truncate(time.Second), ip
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ?, last_ip = ? WHERE id = ?", now.Unix(), ip, se.ID)
	return err
}

func (s *Store) ListSessions(ctx context.Context, userID int64) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sessionCols+" FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC", userID, s.nowUnix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		se, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		if s.now().Before(se.LastSeenAt.Add(se.IdleTimeout)) {
			out = append(out, se)
		}
	}
	return out, rows.Err()
}

func (s *Store) DeleteSession(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// PurgeExpired removes expired sessions, old call records and old audit entries.
func (s *Store) PurgeExpired(ctx context.Context, callRetention, auditRetention time.Duration) error {
	now := s.nowUnix()
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at + idle_seconds <= ?", now, now); err != nil {
		return err
	}
	if callRetention > 0 {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM calls WHERE started_at < ?", now-int64(callRetention/time.Second)); err != nil {
			return err
		}
	}
	if auditRetention > 0 {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM audit_log WHERE ts < ?", now-int64(auditRetention/time.Second)); err != nil {
			return err
		}
	}
	return nil
}
