package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Settings are the admin-editable policies.
type Settings struct {
	// Require2FA: "off", "admins" or "all". Users without 2FA who fall under the policy
	// must enroll right after signing in before they can do anything else.
	Require2FA            string `json:"require2fa"`
	PasswordMinLength     int    `json:"passwordMinLength"`
	WebSessionIdleMinutes int    `json:"webSessionIdleMinutes"`
	WebSessionMaxHours    int    `json:"webSessionMaxHours"`
	AppSessionIdleDays    int    `json:"appSessionIdleDays"`
	AppSessionMaxDays     int    `json:"appSessionMaxDays"`
	MaxCallsPerUser       int    `json:"maxCallsPerUser"`
	CallHistoryDays       int    `json:"callHistoryDays"`
	AuditLogDays          int    `json:"auditLogDays"`
}

func DefaultSettings() Settings {
	return Settings{
		Require2FA:            "off",
		PasswordMinLength:     10,
		WebSessionIdleMinutes: 12 * 60,
		WebSessionMaxHours:    7 * 24,
		AppSessionIdleDays:    30,
		AppSessionMaxDays:     180,
		MaxCallsPerUser:       2,
		CallHistoryDays:       365,
		AuditLogDays:          365,
	}
}

func (st *Settings) Validate() error {
	switch st.Require2FA {
	case "off", "admins", "all":
	default:
		return errors.New("require2fa must be off, admins or all")
	}
	check := func(name string, v, min, max int) error {
		if v < min || v > max {
			return fmt.Errorf("%s must be between %d and %d", name, min, max)
		}
		return nil
	}
	return errors.Join(
		check("passwordMinLength", st.PasswordMinLength, 8, 64),
		check("webSessionIdleMinutes", st.WebSessionIdleMinutes, 5, 30*24*60),
		check("webSessionMaxHours", st.WebSessionMaxHours, 1, 90*24),
		check("appSessionIdleDays", st.AppSessionIdleDays, 1, 365),
		check("appSessionMaxDays", st.AppSessionMaxDays, 1, 730),
		check("maxCallsPerUser", st.MaxCallsPerUser, 1, 10),
		check("callHistoryDays", st.CallHistoryDays, 1, 3650),
		check("auditLogDays", st.AuditLogDays, 30, 3650),
	)
}

const settingsKey = "policy"

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	st := DefaultSettings()
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", settingsKey).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return st, nil
		}
		return st, err
	}
	// Unknown/missing fields keep their defaults.
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return DefaultSettings(), err
	}
	if st.Validate() != nil {
		return DefaultSettings(), nil
	}
	return st, nil
}

func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	if err := st.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	_, err := s.db.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		settingsKey, string(b))
	return err
}
