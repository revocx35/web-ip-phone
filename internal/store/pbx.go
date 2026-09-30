package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type PBX struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	Transport      string    `json:"transport"` // udp | tcp | tls
	Domain         string    `json:"domain"`    // SIP domain for URIs; "" = Host
	TLSVerify      bool      `json:"tlsVerify"`
	Codecs         []string  `json:"codecs"`   // preference order: PCMU, PCMA
	DTMFMode       string    `json:"dtmfMode"` // rfc4733 | info
	RegisterExpiry int       `json:"registerExpiry"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// SIPDomain is the host part used in From/To/Request-URIs.
func (p *PBX) SIPDomain() string {
	if p.Domain != "" {
		return p.Domain
	}
	return p.Host
}

const pbxCols = "id, name, host, port, transport, domain, tls_verify, codecs, dtmf_mode, register_expiry, enabled, created_at, updated_at"

func scanPBX(row interface{ Scan(...any) error }) (*PBX, error) {
	var p PBX
	var codecs string
	var ca, ua int64
	err := row.Scan(&p.ID, &p.Name, &p.Host, &p.Port, &p.Transport, &p.Domain, &p.TLSVerify, &codecs, &p.DTMFMode,
		&p.RegisterExpiry, &p.Enabled, &ca, &ua)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Codecs = strings.Split(codecs, ",")
	p.CreatedAt, p.UpdatedAt = unix(ca), unix(ua)
	return &p, nil
}

func (s *Store) ListPBXs(ctx context.Context) ([]*PBX, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pbxCols+" FROM pbxs ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PBX
	for rows.Next() {
		p, err := scanPBX(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPBX(ctx context.Context, id int64) (*PBX, error) {
	return scanPBX(s.db.QueryRowContext(ctx, "SELECT "+pbxCols+" FROM pbxs WHERE id = ?", id))
}

func (s *Store) CreatePBX(ctx context.Context, p *PBX) (*PBX, error) {
	now := s.nowUnix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO pbxs (name, host, port, transport, domain, tls_verify, codecs, dtmf_mode,
		register_expiry, enabled, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Host, p.Port, p.Transport, p.Domain, b2i(p.TLSVerify), strings.Join(p.Codecs, ","), p.DTMFMode,
		p.RegisterExpiry, b2i(p.Enabled), now, now)
	if isUniqueErr(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetPBX(ctx, id)
}

func (s *Store) UpdatePBX(ctx context.Context, p *PBX) error {
	res, err := s.db.ExecContext(ctx, `UPDATE pbxs SET name = ?, host = ?, port = ?, transport = ?, domain = ?, tls_verify = ?,
		codecs = ?, dtmf_mode = ?, register_expiry = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Host, p.Port, p.Transport, p.Domain, b2i(p.TLSVerify), strings.Join(p.Codecs, ","), p.DTMFMode,
		p.RegisterExpiry, b2i(p.Enabled), s.nowUnix(), p.ID)
	if isUniqueErr(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeletePBX(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM pbxs WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
