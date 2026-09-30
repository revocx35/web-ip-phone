package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID                 int64     `json:"id"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"displayName"`
	IsAdmin            bool      `json:"isAdmin"`
	Disabled           bool      `json:"disabled"`
	TOTPEnabled        bool      `json:"totpEnabled"`
	MustChangePassword bool      `json:"mustChangePassword"`
	PasswordChangedAt  time.Time `json:"passwordChangedAt"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`

	PasswordHash string `json:"-"`
	TOTPSecret   []byte `json:"-"` // encrypted
	TOTPLastStep int64  `json:"-"`
}

const userCols = `id, username, display_name, password_hash, is_admin, disabled, totp_secret, totp_enabled,
	totp_last_step, must_change_password, password_changed_at, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var pwc, ca, ua int64
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &u.IsAdmin, &u.Disabled, &u.TOTPSecret,
		&u.TOTPEnabled, &u.TOTPLastStep, &u.MustChangePassword, &pwc, &ca, &ua)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.PasswordChangedAt, u.CreatedAt, u.UpdatedAt = unix(pwc), unix(ca), unix(ua)
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

type NewUser struct {
	Username           string
	DisplayName        string
	PasswordHash       string
	IsAdmin            bool
	MustChangePassword bool
}

func (s *Store) CreateUser(ctx context.Context, nu NewUser) (*User, error) {
	now := s.nowUnix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (username, display_name, password_hash, is_admin,
		must_change_password, password_changed_at, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		nu.Username, nu.DisplayName, nu.PasswordHash, b2i(nu.IsAdmin), b2i(nu.MustChangePassword), now, now, now)
	if isUniqueErr(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUser(ctx, id)
}

// CreateFirstAdmin creates the initial admin only while the users table is empty, atomically.
func (s *Store) CreateFirstAdmin(ctx context.Context, nu NewUser) (*User, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrConflict
		}
		now := s.nowUnix()
		res, err := tx.ExecContext(ctx, `INSERT INTO users (username, display_name, password_hash, is_admin,
			password_changed_at, created_at, updated_at) VALUES (?,?,?,1,?,?,?)`,
			nu.Username, nu.DisplayName, nu.PasswordHash, now, now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, id)
}

func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE username = ?", username))
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userCols+" FROM users ORDER BY username COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type UserUpdate struct {
	DisplayName *string
	IsAdmin     *bool
	Disabled    *bool
}

// UpdateUser applies the non-nil fields. Removing the admin role or disabling the last
// enabled admin is refused with ErrLastAdmin.
func (s *Store) UpdateUser(ctx context.Context, id int64, up UserUpdate) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if (up.IsAdmin != nil && !*up.IsAdmin) || (up.Disabled != nil && *up.Disabled) {
			if err := ensureOtherAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		now := s.nowUnix()
		if up.DisplayName != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?", *up.DisplayName, now, id); err != nil {
				return err
			}
		}
		if up.IsAdmin != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE users SET is_admin = ?, updated_at = ? WHERE id = ?", b2i(*up.IsAdmin), now, id); err != nil {
				return err
			}
		}
		if up.Disabled != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?", b2i(*up.Disabled), now, id); err != nil {
				return err
			}
			if *up.Disabled {
				if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

var ErrLastAdmin = errors.New("at least one enabled admin account must remain")

// ensureOtherAdmin fails when user id is the only enabled admin.
func ensureOtherAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE is_admin = 1 AND disabled = 0 AND id != ?", id).Scan(&n)
	if err != nil {
		return err
	}
	var isAdmin, disabled bool
	err = tx.QueryRowContext(ctx, "SELECT is_admin, disabled FROM users WHERE id = ?", id).Scan(&isAdmin, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if isAdmin && !disabled && n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := ensureOtherAdmin(ctx, tx, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SetPassword stores a new hash and ends all sessions of the user except keepSession (0 = none).
func (s *Store) SetPassword(ctx context.Context, id int64, hash string, mustChange bool, keepSession int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.nowUnix()
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, must_change_password = ?,
			password_changed_at = ?, updated_at = ? WHERE id = ?`, hash, b2i(mustChange), now, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND id != ?", id, keepSession)
		return err
	})
}

// UpgradePasswordHash replaces the hash after a successful login with outdated parameters.
func (s *Store) UpgradePasswordHash(ctx context.Context, id int64, oldHash, newHash string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ?", newHash, id, oldHash)
	return err
}

// SetTOTP stores an encrypted TOTP secret. enabled=false keeps it pending until confirmed.
func (s *Store) SetTOTP(ctx context.Context, id int64, secret []byte, enabled bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET totp_secret = ?, totp_enabled = ?, totp_last_step = 0, updated_at = ? WHERE id = ?",
		secret, b2i(enabled), s.nowUnix(), id)
	return err
}

// EnableTOTP marks the pending secret as active and stores the recovery code hashes.
func (s *Store) EnableTOTP(ctx context.Context, id int64, lastStep int64, recoveryHashes [][]byte) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE users SET totp_enabled = 1, totp_last_step = ?, updated_at = ? WHERE id = ? AND totp_secret IS NOT NULL",
			lastStep, s.nowUnix(), id); err != nil {
			return err
		}
		return replaceRecoveryCodes(ctx, tx, id, recoveryHashes)
	})
}

func (s *Store) DisableTOTP(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE users SET totp_secret = NULL, totp_enabled = 0, totp_last_step = 0, updated_at = ? WHERE id = ?",
			s.nowUnix(), id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE user_id = ?", id)
		return err
	})
}

// UseTOTPStep records a used time step; it fails (false) when the step is not newer than the
// last one, which blocks replay of a code within its validity window.
func (s *Store) UseTOTPStep(ctx context.Context, id int64, step int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?", step, id, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func replaceRecoveryCodes(ctx context.Context, tx *sql.Tx, id int64, hashes [][]byte) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE user_id = ?", id); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)", id, h); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, id int64, hashes [][]byte) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return replaceRecoveryCodes(ctx, tx, id, hashes) })
}

// UseRecoveryCode consumes a matching unused recovery code (codes are high-entropy, so a
// plain SHA-256 is an adequate hash).
func (s *Store) UseRecoveryCode(ctx context.Context, id int64, code string) (bool, error) {
	sum := sha256.Sum256([]byte(code))
	var used bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id, code_hash FROM recovery_codes WHERE user_id = ? AND used_at IS NULL", id)
		if err != nil {
			return err
		}
		var match int64
		for rows.Next() {
			var rid int64
			var h []byte
			if err := rows.Scan(&rid, &h); err != nil {
				rows.Close()
				return err
			}
			if subtle.ConstantTimeCompare(h, sum[:]) == 1 {
				match = rid
			}
		}
		rows.Close()
		if match == 0 {
			return nil
		}
		used = true
		_, err = tx.ExecContext(ctx, "UPDATE recovery_codes SET used_at = ? WHERE id = ?", s.nowUnix(), match)
		return err
	})
	return used, err
}

func (s *Store) CountRecoveryCodes(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL", id).Scan(&n)
	return n, err
}
