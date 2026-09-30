package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Phone is a virtual phone: a SIP account (extension) on a PBX. Admin-provisioned phones
// have OwnerID 0 and are granted to users through phone_access; user-owned phones exist on
// PBXs where the owner has "any credentials" access.
type Phone struct {
	ID           int64     `json:"id"`
	PBXID        int64     `json:"pbxId"`
	OwnerID      int64     `json:"ownerId"` // 0 = admin-provisioned
	Label        string    `json:"label"`
	SIPUser      string    `json:"sipUser"`
	AuthUser     string    `json:"authUser"`
	DisplayName  string    `json:"displayName"`
	Register     bool      `json:"register"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Secret       []byte    `json:"-"` // encrypted with context SecretContext()
	ContactToken string    `json:"-"`
}

// SecretContext binds the encrypted SIP password to this phone row.
func (p *Phone) SecretContext() string { return "phone-secret:" + p.ContactToken }

// DigestUser is the username for digest authentication.
func (p *Phone) DigestUser() string {
	if p.AuthUser != "" {
		return p.AuthUser
	}
	return p.SIPUser
}

const phoneCols = "p.id, p.pbx_id, p.owner_id, p.label, p.sip_user, p.auth_user, p.display_name, p.register, p.created_at, p.updated_at, p.secret, p.contact_token"

func scanPhone(row interface{ Scan(...any) error }) (*Phone, error) {
	var p Phone
	var owner sql.NullInt64
	var ca, ua int64
	err := row.Scan(&p.ID, &p.PBXID, &owner, &p.Label, &p.SIPUser, &p.AuthUser, &p.DisplayName, &p.Register, &ca, &ua, &p.Secret, &p.ContactToken)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.OwnerID = owner.Int64
	p.CreatedAt, p.UpdatedAt = unix(ca), unix(ua)
	return &p, nil
}

func (s *Store) queryPhones(ctx context.Context, q string, args ...any) ([]*Phone, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Phone
	for rows.Next() {
		p, err := scanPhone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPhone(ctx context.Context, id int64) (*Phone, error) {
	return scanPhone(s.db.QueryRowContext(ctx, "SELECT "+phoneCols+" FROM phones p WHERE p.id = ?", id))
}

func (s *Store) PhoneByContactToken(ctx context.Context, token string) (*Phone, error) {
	return scanPhone(s.db.QueryRowContext(ctx, "SELECT "+phoneCols+" FROM phones p WHERE p.contact_token = ?", token))
}

// ListProvisionedPhones lists admin-provisioned phones (all PBXs when pbxID is 0).
func (s *Store) ListProvisionedPhones(ctx context.Context, pbxID int64) ([]*Phone, error) {
	if pbxID == 0 {
		return s.queryPhones(ctx, "SELECT "+phoneCols+" FROM phones p WHERE p.owner_id IS NULL ORDER BY p.pbx_id, p.sip_user")
	}
	return s.queryPhones(ctx, "SELECT "+phoneCols+" FROM phones p WHERE p.owner_id IS NULL AND p.pbx_id = ? ORDER BY p.sip_user", pbxID)
}

// ListAllPhones lists every phone (admin overview).
func (s *Store) ListAllPhones(ctx context.Context) ([]*Phone, error) {
	return s.queryPhones(ctx, "SELECT "+phoneCols+" FROM phones p ORDER BY p.pbx_id, p.sip_user")
}

// ListOwnedPhones lists the phones a user created.
func (s *Store) ListOwnedPhones(ctx context.Context, userID int64) ([]*Phone, error) {
	return s.queryPhones(ctx, "SELECT "+phoneCols+" FROM phones p WHERE p.owner_id = ? ORDER BY p.label, p.sip_user", userID)
}

// usableSQL selects the phones user ? may use right now: the PBX is enabled, the user has
// access to it, and the phone is either their own (with "any credentials" access) or an
// admin-provisioned phone granted to them.
const usableSQL = `SELECT ` + phoneCols + ` FROM phones p
	JOIN pbxs x ON x.id = p.pbx_id AND x.enabled = 1
	JOIN pbx_access a ON a.pbx_id = p.pbx_id AND a.user_id = ?1
	WHERE ((p.owner_id = ?1 AND a.mode = 'any')
	   OR (p.owner_id IS NULL AND EXISTS (SELECT 1 FROM phone_access pa WHERE pa.phone_id = p.id AND pa.user_id = ?1)))`

// UsablePhones lists the phones a user may use.
func (s *Store) UsablePhones(ctx context.Context, userID int64) ([]*Phone, error) {
	return s.queryPhones(ctx, usableSQL+" ORDER BY x.name COLLATE NOCASE, p.label, p.sip_user", userID)
}

// UsablePhone returns the phone if the user may use it, else ErrNotFound.
func (s *Store) UsablePhone(ctx context.Context, userID, phoneID int64) (*Phone, error) {
	return scanPhone(s.db.QueryRowContext(ctx, usableSQL+" AND p.id = ?2", userID, phoneID))
}

func (s *Store) CreatePhone(ctx context.Context, p *Phone) (*Phone, error) {
	now := s.nowUnix()
	var owner any
	if p.OwnerID != 0 {
		owner = p.OwnerID
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO phones (pbx_id, owner_id, label, sip_user, auth_user, secret, display_name,
		register, contact_token, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.PBXID, owner, p.Label, p.SIPUser, p.AuthUser, p.Secret, p.DisplayName, b2i(p.Register), p.ContactToken, now, now)
	if isUniqueErr(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetPhone(ctx, id)
}

// UpdatePhone saves label, SIP identity and flags; Secret is replaced only when non-nil.
func (s *Store) UpdatePhone(ctx context.Context, p *Phone) error {
	var res sql.Result
	var err error
	if p.Secret != nil {
		res, err = s.db.ExecContext(ctx, `UPDATE phones SET label = ?, sip_user = ?, auth_user = ?, display_name = ?, register = ?,
			secret = ?, updated_at = ? WHERE id = ?`, p.Label, p.SIPUser, p.AuthUser, p.DisplayName, b2i(p.Register), p.Secret, s.nowUnix(), p.ID)
	} else {
		res, err = s.db.ExecContext(ctx, `UPDATE phones SET label = ?, sip_user = ?, auth_user = ?, display_name = ?, register = ?,
			updated_at = ? WHERE id = ?`, p.Label, p.SIPUser, p.AuthUser, p.DisplayName, b2i(p.Register), s.nowUnix(), p.ID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeletePhone(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM phones WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const (
	AccessAny      = "any"      // any SIP credentials the user knows
	AccessSelected = "selected" // only admin-provisioned phones granted to the user
)

// Access is a user's permission on one PBX.
type Access struct {
	PBXID     int64   `json:"pbxId"`
	Mode      string  `json:"mode"`
	DialRules string  `json:"dialRules"` // newline-separated patterns; "" = unrestricted
	PhoneIDs  []int64 `json:"phoneIds"`  // admin-provisioned phones granted on this PBX
}

func (s *Store) UserAccess(ctx context.Context, userID int64) ([]*Access, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.pbx_id, a.mode, a.dial_rules FROM pbx_access a JOIN pbxs x ON x.id = a.pbx_id
		WHERE a.user_id = ? ORDER BY x.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	var out []*Access
	byPBX := map[int64]*Access{}
	for rows.Next() {
		a := &Access{PhoneIDs: []int64{}}
		if err := rows.Scan(&a.PBXID, &a.Mode, &a.DialRules); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
		byPBX[a.PBXID] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	prow, err := s.db.QueryContext(ctx, `SELECT p.pbx_id, p.id FROM phone_access pa JOIN phones p ON p.id = pa.phone_id
		WHERE pa.user_id = ? ORDER BY p.sip_user`, userID)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var pbx, phone int64
		if err := prow.Scan(&pbx, &phone); err != nil {
			return nil, err
		}
		if a := byPBX[pbx]; a != nil {
			a.PhoneIDs = append(a.PhoneIDs, phone)
		}
	}
	return out, prow.Err()
}

// AccessFor returns the user's access to a PBX, or ErrNotFound.
func (s *Store) AccessFor(ctx context.Context, userID, pbxID int64) (*Access, error) {
	a := &Access{PBXID: pbxID}
	err := s.db.QueryRowContext(ctx, "SELECT mode, dial_rules FROM pbx_access WHERE user_id = ? AND pbx_id = ?", userID, pbxID).Scan(&a.Mode, &a.DialRules)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// SetUserAccess replaces all PBX and phone grants of a user. Phone IDs must be
// admin-provisioned phones on the PBX they are listed under.
func (s *Store) SetUserAccess(ctx context.Context, userID int64, access []*Access) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM pbx_access WHERE user_id = ?", userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM phone_access WHERE user_id = ?", userID); err != nil {
			return err
		}
		for _, a := range access {
			if _, err := tx.ExecContext(ctx, "INSERT INTO pbx_access (user_id, pbx_id, mode, dial_rules) VALUES (?,?,?,?)",
				userID, a.PBXID, a.Mode, a.DialRules); err != nil {
				if isUniqueErr(err) {
					return ErrConflict
				}
				return err
			}
			for _, pid := range a.PhoneIDs {
				var n int
				if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM phones WHERE id = ? AND pbx_id = ? AND owner_id IS NULL",
					pid, a.PBXID).Scan(&n); err != nil {
					return err
				}
				if n != 1 {
					return ErrNotFound
				}
				if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO phone_access (user_id, phone_id) VALUES (?,?)", userID, pid); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// PhoneUsers lists the IDs of users that may currently use a phone.
func (s *Store) PhoneUsers(ctx context.Context, phoneID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id FROM users u JOIN phones p ON p.id = ?1
		JOIN pbxs x ON x.id = p.pbx_id AND x.enabled = 1
		JOIN pbx_access a ON a.pbx_id = p.pbx_id AND a.user_id = u.id
		WHERE u.disabled = 0 AND ((p.owner_id = u.id AND a.mode = 'any')
		   OR (p.owner_id IS NULL AND EXISTS (SELECT 1 FROM phone_access pa WHERE pa.phone_id = p.id AND pa.user_id = u.id)))`, phoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
