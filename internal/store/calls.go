package store

import (
	"context"
	"database/sql"
	"time"
)

// CallRecord is one entry of the call history.
type CallRecord struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"userId"`
	PhoneID    int64      `json:"phoneId"`
	Username   string     `json:"username"`
	PhoneLabel string     `json:"phoneLabel"`
	PBXName    string     `json:"pbxName"`
	Direction  string     `json:"direction"` // out | in
	Remote     string     `json:"remote"`
	RemoteName string     `json:"remoteName"`
	StartedAt  time.Time  `json:"startedAt"`
	AnsweredAt *time.Time `json:"answeredAt"`
	EndedAt    *time.Time `json:"endedAt"`
	Status     string     `json:"status"` // answered | missed | rejected | busy | failed | cancelled | unanswered
	Reason     string     `json:"reason"`
	SIPCode    int        `json:"sipCode"`
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// InsertCall stores a call when it starts and returns its ID.
func (s *Store) InsertCall(ctx context.Context, c *CallRecord) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO calls (user_id, phone_id, username, phone_label, pbx_name, direction, remote,
		remote_name, started_at, status) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		nullID(c.UserID), nullID(c.PhoneID), c.Username, c.PhoneLabel, c.PBXName, c.Direction, c.Remote, c.RemoteName,
		c.StartedAt.Unix(), c.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateCall stores the outcome of a call.
func (s *Store) UpdateCall(ctx context.Context, c *CallRecord) error {
	var ans, end any
	if c.AnsweredAt != nil {
		ans = c.AnsweredAt.Unix()
	}
	if c.EndedAt != nil {
		end = c.EndedAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET user_id = ?, username = ?, answered_at = ?, ended_at = ?, status = ?, reason = ?,
		sip_code = ?, remote_name = ? WHERE id = ?`, nullID(c.UserID), c.Username, ans, end, c.Status, c.Reason, c.SIPCode, c.RemoteName, c.ID)
	return err
}

const callCols = "id, user_id, phone_id, username, phone_label, pbx_name, direction, remote, remote_name, started_at, answered_at, ended_at, status, reason, sip_code"

func (s *Store) queryCalls(ctx context.Context, q string, args ...any) ([]*CallRecord, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CallRecord{}
	for rows.Next() {
		var c CallRecord
		var uid, pid, ans, end sql.NullInt64
		var st int64
		if err := rows.Scan(&c.ID, &uid, &pid, &c.Username, &c.PhoneLabel, &c.PBXName, &c.Direction, &c.Remote, &c.RemoteName,
			&st, &ans, &end, &c.Status, &c.Reason, &c.SIPCode); err != nil {
			return nil, err
		}
		c.UserID, c.PhoneID = uid.Int64, pid.Int64
		c.StartedAt, c.AnsweredAt, c.EndedAt = unix(st), nullUnix(ans), nullUnix(end)
		out = append(out, &c)
	}
	return out, rows.Err()
}

// UserCalls lists a user's own calls plus unanswered incoming calls on phones they can use.
func (s *Store) UserCalls(ctx context.Context, userID int64, limit int) ([]*CallRecord, error) {
	return s.queryCalls(ctx, `SELECT `+callCols+` FROM calls WHERE (user_id = ?1
		OR (user_id IS NULL AND direction = 'in' AND phone_id IN (SELECT p.id FROM (`+usableSQL+`) p)))
		AND started_at > (SELECT calls_cleared_at FROM users WHERE id = ?1)
		ORDER BY started_at DESC, id DESC LIMIT ?2`, userID, limit)
}

// AllCalls lists recent calls of all users (admin).
func (s *Store) AllCalls(ctx context.Context, limit int) ([]*CallRecord, error) {
	return s.queryCalls(ctx, "SELECT "+callCols+" FROM calls ORDER BY started_at DESC, id DESC LIMIT ?", limit)
}

// ClearUserCalls hides all calls up to now from a user's history (the admin call log
// keeps them until they expire).
func (s *Store) ClearUserCalls(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET calls_cleared_at = ? WHERE id = ?", s.nowUnix(), userID)
	return err
}

// FinishDanglingCalls marks calls left open by a crash or restart as ended.
func (s *Store) FinishDanglingCalls(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE calls SET ended_at = started_at, status = CASE WHEN answered_at IS NULL THEN 'failed' ELSE 'answered' END,
		reason = 'server restarted' WHERE ended_at IS NULL`)
	return err
}
