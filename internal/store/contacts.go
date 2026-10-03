package store

import (
	"context"
	"database/sql"
	"time"
)

// ContactNumber is one phone number of a contact.
type ContactNumber struct {
	Label  string `json:"label"`
	Number string `json:"number"`
}

// Contact is a phone book entry: a user's own (OwnerID = user) or shared with all users
// (OwnerID 0, managed by admins). Favorite is per user, also for shared contacts.
type Contact struct {
	ID        int64           `json:"id"`
	OwnerID   int64           `json:"-"`
	Name      string          `json:"name"`
	Numbers   []ContactNumber `json:"numbers"`
	Favorite  bool            `json:"favorite"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// Shared reports whether the contact belongs to the shared phone book.
func (c *Contact) Shared() bool { return c.OwnerID == 0 }

// visibleContactSQL: contacts user ?1 can see (own + shared).
const visibleContactSQL = "(c.owner_id = ?1 OR c.owner_id IS NULL)"

// ListContacts returns the user's own and the shared contacts, sorted by name.
func (s *Store) ListContacts(ctx context.Context, userID int64) ([]*Contact, error) {
	return s.queryContacts(ctx, userID, "")
}

// GetContact returns a contact the user can see, else ErrNotFound.
func (s *Store) GetContact(ctx context.Context, userID, id int64) (*Contact, error) {
	list, err := s.queryContacts(ctx, userID, " AND c.id = ?2", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) queryContacts(ctx context.Context, userID int64, where string, args ...any) ([]*Contact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.owner_id, c.name, c.created_at, c.updated_at, f.user_id IS NOT NULL
		FROM contacts c LEFT JOIN contact_favorites f ON f.contact_id = c.id AND f.user_id = ?1
		WHERE `+visibleContactSQL+where+` ORDER BY c.name COLLATE NOCASE, c.id`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	out := []*Contact{}
	byID := map[int64]*Contact{}
	for rows.Next() {
		c := &Contact{Numbers: []ContactNumber{}}
		var owner sql.NullInt64
		var ca, ua int64
		if err := rows.Scan(&c.ID, &owner, &c.Name, &ca, &ua, &c.Favorite); err != nil {
			rows.Close()
			return nil, err
		}
		c.OwnerID = owner.Int64
		c.CreatedAt, c.UpdatedAt = unix(ca), unix(ua)
		out = append(out, c)
		byID[c.ID] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	nrows, err := s.db.QueryContext(ctx, `SELECT n.contact_id, n.label, n.number FROM contact_numbers n
		JOIN contacts c ON c.id = n.contact_id WHERE `+visibleContactSQL+where+` ORDER BY n.contact_id, n.position`,
		append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer nrows.Close()
	for nrows.Next() {
		var id int64
		var n ContactNumber
		if err := nrows.Scan(&id, &n.Label, &n.Number); err != nil {
			return nil, err
		}
		if c := byID[id]; c != nil {
			c.Numbers = append(c.Numbers, n)
		}
	}
	return out, nrows.Err()
}

// CountContacts counts a user's own contacts, or the shared ones for ownerID 0.
func (s *Store) CountContacts(ctx context.Context, ownerID int64) (int, error) {
	var n int
	var err error
	if ownerID == 0 {
		err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM contacts WHERE owner_id IS NULL").Scan(&n)
	} else {
		err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM contacts WHERE owner_id = ?", ownerID).Scan(&n)
	}
	return n, err
}

func insertNumbers(ctx context.Context, tx *sql.Tx, id int64, numbers []ContactNumber) error {
	for i, n := range numbers {
		if _, err := tx.ExecContext(ctx, "INSERT INTO contact_numbers (contact_id, position, label, number) VALUES (?,?,?,?)",
			id, i, n.Label, n.Number); err != nil {
			return err
		}
	}
	return nil
}

// CreateContact stores a new contact (OwnerID 0 = shared) and returns its ID.
func (s *Store) CreateContact(ctx context.Context, c *Contact) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.nowUnix()
		res, err := tx.ExecContext(ctx, "INSERT INTO contacts (owner_id, name, created_at, updated_at) VALUES (?,?,?,?)",
			nullID(c.OwnerID), c.Name, now, now)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return insertNumbers(ctx, tx, id, c.Numbers)
	})
	return id, err
}

// UpdateContact replaces name, owner and numbers of a contact. A contact that stops being
// shared loses the favorite marks of everyone but its new owner.
func (s *Store) UpdateContact(ctx context.Context, c *Contact) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE contacts SET owner_id = ?, name = ?, updated_at = ? WHERE id = ?",
			nullID(c.OwnerID), c.Name, s.nowUnix(), c.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if c.OwnerID != 0 {
			if _, err := tx.ExecContext(ctx, "DELETE FROM contact_favorites WHERE contact_id = ? AND user_id != ?", c.ID, c.OwnerID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM contact_numbers WHERE contact_id = ?", c.ID); err != nil {
			return err
		}
		return insertNumbers(ctx, tx, c.ID, c.Numbers)
	})
}

func (s *Store) DeleteContact(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM contacts WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetFavorite marks or unmarks a contact the user can see as one of their favorites.
func (s *Store) SetFavorite(ctx context.Context, userID, contactID int64, on bool) error {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM contacts c WHERE "+visibleContactSQL+" AND c.id = ?2",
		userID, contactID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	var err error
	if on {
		_, err = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO contact_favorites (user_id, contact_id) VALUES (?,?)", userID, contactID)
	} else {
		_, err = s.db.ExecContext(ctx, "DELETE FROM contact_favorites WHERE user_id = ? AND contact_id = ?", userID, contactID)
	}
	return err
}
