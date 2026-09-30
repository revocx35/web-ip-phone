package store

import (
	"context"
	"database/sql"
	"time"
)

type AuditEntry struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time"`
	UserID   int64     `json:"userId"`
	Username string    `json:"username"`
	IP       string    `json:"ip"`
	Action   string    `json:"action"`
	Target   string    `json:"target"`
	Details  string    `json:"details"`
	Success  bool      `json:"success"`
}

func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (ts, user_id, username, ip, action, target, details, success)
		VALUES (?,?,?,?,?,?,?,?)`, s.nowUnix(), nullID(e.UserID), e.Username, e.IP, e.Action, e.Target, e.Details, b2i(e.Success))
	return err
}

// ListAudit returns entries older than beforeID (0 = newest), newest first.
func (s *Store) ListAudit(ctx context.Context, beforeID int64, limit int) ([]*AuditEntry, error) {
	if beforeID <= 0 {
		beforeID = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, user_id, username, ip, action, target, details, success FROM audit_log
		WHERE id < ? ORDER BY id DESC LIMIT ?`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var ts int64
		var uid sql.NullInt64
		if err := rows.Scan(&e.ID, &ts, &uid, &e.Username, &e.IP, &e.Action, &e.Target, &e.Details, &e.Success); err != nil {
			return nil, err
		}
		e.Time, e.UserID = unix(ts), uid.Int64
		out = append(out, &e)
	}
	return out, rows.Err()
}
