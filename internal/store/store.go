// Package store persists users, sessions, PBXs, virtual phones, access grants, call
// history and the audit log in SQLite (pure Go driver, no cgo).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (and migrates) the database file.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serializes all access: no SQLITE_BUSY, and the load is tiny.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory opens a private in-memory database (tests).
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// SetClock replaces the time source (tests).
func (s *Store) SetClock(now func() time.Time) { s.now = now }

func (s *Store) nowUnix() int64 { return s.now().Unix() }

var migrations = []string{
	// v1
	`
CREATE TABLE users (
	id                   INTEGER PRIMARY KEY,
	username             TEXT NOT NULL UNIQUE COLLATE NOCASE,
	display_name         TEXT NOT NULL DEFAULT '',
	password_hash        TEXT NOT NULL,
	is_admin             INTEGER NOT NULL DEFAULT 0,
	disabled             INTEGER NOT NULL DEFAULT 0,
	totp_secret          BLOB,
	totp_enabled         INTEGER NOT NULL DEFAULT 0,
	totp_last_step       INTEGER NOT NULL DEFAULT 0,
	must_change_password INTEGER NOT NULL DEFAULT 0,
	calls_cleared_at     INTEGER NOT NULL DEFAULT 0,
	password_changed_at  INTEGER NOT NULL,
	created_at           INTEGER NOT NULL,
	updated_at           INTEGER NOT NULL
);
CREATE TABLE recovery_codes (
	id        INTEGER PRIMARY KEY,
	user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	code_hash BLOB NOT NULL,
	used_at   INTEGER
);
CREATE INDEX recovery_codes_user ON recovery_codes(user_id);
CREATE TABLE sessions (
	id           INTEGER PRIMARY KEY,
	token_hash   BLOB NOT NULL UNIQUE,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind         TEXT NOT NULL,
	name         TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL,
	expires_at   INTEGER NOT NULL,
	idle_seconds INTEGER NOT NULL,
	created_ip   TEXT NOT NULL DEFAULT '',
	last_ip      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE pbxs (
	id              INTEGER PRIMARY KEY,
	name            TEXT NOT NULL UNIQUE COLLATE NOCASE,
	host            TEXT NOT NULL,
	port            INTEGER NOT NULL,
	transport       TEXT NOT NULL,
	domain          TEXT NOT NULL DEFAULT '',
	tls_verify      INTEGER NOT NULL DEFAULT 1,
	codecs          TEXT NOT NULL DEFAULT 'PCMU,PCMA',
	dtmf_mode       TEXT NOT NULL DEFAULT 'rfc4733',
	register_expiry INTEGER NOT NULL DEFAULT 300,
	enabled         INTEGER NOT NULL DEFAULT 1,
	created_at      INTEGER NOT NULL,
	updated_at      INTEGER NOT NULL
);
CREATE TABLE phones (
	id            INTEGER PRIMARY KEY,
	pbx_id        INTEGER NOT NULL REFERENCES pbxs(id) ON DELETE CASCADE,
	owner_id      INTEGER REFERENCES users(id) ON DELETE CASCADE,
	label         TEXT NOT NULL DEFAULT '',
	sip_user      TEXT NOT NULL,
	auth_user     TEXT NOT NULL DEFAULT '',
	secret        BLOB NOT NULL,
	display_name  TEXT NOT NULL DEFAULT '',
	register      INTEGER NOT NULL DEFAULT 1,
	contact_token TEXT NOT NULL UNIQUE,
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);
CREATE INDEX phones_pbx ON phones(pbx_id);
CREATE INDEX phones_owner ON phones(owner_id);
CREATE TABLE pbx_access (
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	pbx_id     INTEGER NOT NULL REFERENCES pbxs(id) ON DELETE CASCADE,
	mode       TEXT NOT NULL,
	dial_rules TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (user_id, pbx_id)
);
CREATE TABLE phone_access (
	user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	phone_id INTEGER NOT NULL REFERENCES phones(id) ON DELETE CASCADE,
	PRIMARY KEY (user_id, phone_id)
);
CREATE INDEX phone_access_phone ON phone_access(phone_id);
CREATE TABLE calls (
	id          INTEGER PRIMARY KEY,
	user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
	phone_id    INTEGER REFERENCES phones(id) ON DELETE SET NULL,
	username    TEXT NOT NULL DEFAULT '',
	phone_label TEXT NOT NULL DEFAULT '',
	pbx_name    TEXT NOT NULL DEFAULT '',
	direction   TEXT NOT NULL,
	remote      TEXT NOT NULL DEFAULT '',
	remote_name TEXT NOT NULL DEFAULT '',
	started_at  INTEGER NOT NULL,
	answered_at INTEGER,
	ended_at    INTEGER,
	status      TEXT NOT NULL DEFAULT '',
	reason      TEXT NOT NULL DEFAULT '',
	sip_code    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX calls_user ON calls(user_id, started_at);
CREATE INDEX calls_phone ON calls(phone_id, started_at);
CREATE INDEX calls_started ON calls(started_at);
CREATE TABLE audit_log (
	id       INTEGER PRIMARY KEY,
	ts       INTEGER NOT NULL,
	user_id  INTEGER,
	username TEXT NOT NULL DEFAULT '',
	ip       TEXT NOT NULL DEFAULT '',
	action   TEXT NOT NULL,
	target   TEXT NOT NULL DEFAULT '',
	details  TEXT NOT NULL DEFAULT '',
	success  INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX audit_ts ON audit_log(ts);
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`,
	// v2: contacts (owner_id NULL = shared with all users, managed by admins)
	`
CREATE TABLE contacts (
	id         INTEGER PRIMARY KEY,
	owner_id   INTEGER REFERENCES users(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX contacts_owner ON contacts(owner_id);
CREATE TABLE contact_numbers (
	contact_id INTEGER NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
	position   INTEGER NOT NULL,
	label      TEXT NOT NULL DEFAULT '',
	number     TEXT NOT NULL,
	PRIMARY KEY (contact_id, position)
);
CREATE TABLE contact_favorites (
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	contact_id INTEGER NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
	PRIMARY KEY (user_id, contact_id)
);
CREATE INDEX contact_favorites_contact ON contact_favorites(contact_id);
`,
}

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema v%d is newer than this server (v%d): refusing to downgrade", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, f func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func isUniqueErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func unix(t int64) time.Time {
	if t == 0 {
		return time.Time{}
	}
	return time.Unix(t, 0).UTC()
}

func nullUnix(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := unix(n.Int64)
	return &t
}
