package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mustStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func user(t *testing.T, s *Store, name string, admin bool) *User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), NewUser{Username: name, PasswordHash: "x", IsAdmin: admin})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func ids(ps []*Phone) map[int64]bool {
	m := map[int64]bool{}
	for _, p := range ps {
		m[p.ID] = true
	}
	return m
}

func TestAccessRules(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	alice, bob := user(t, s, "alice", false), user(t, s, "bob", false)
	pbx, _ := s.CreatePBX(ctx, &PBX{Name: "A", Host: "10.0.0.1", Port: 5060, Transport: "udp", Codecs: []string{"PCMU"}, DTMFMode: "rfc4733", RegisterExpiry: 300, Enabled: true})
	pbx2, _ := s.CreatePBX(ctx, &PBX{Name: "B", Host: "10.0.0.2", Port: 5060, Transport: "udp", Codecs: []string{"PCMU"}, DTMFMode: "rfc4733", RegisterExpiry: 300, Enabled: true})
	ext1, _ := s.CreatePhone(ctx, &Phone{PBXID: pbx.ID, SIPUser: "1001", Secret: []byte("s"), ContactToken: "t1"})
	ext2, _ := s.CreatePhone(ctx, &Phone{PBXID: pbx.ID, SIPUser: "1002", Secret: []byte("s"), ContactToken: "t2"})
	own, _ := s.CreatePhone(ctx, &Phone{PBXID: pbx.ID, OwnerID: bob.ID, SIPUser: "1003", Secret: []byte("s"), ContactToken: "t3"})

	// no access at all
	if ps, _ := s.UsablePhones(ctx, alice.ID); len(ps) != 0 {
		t.Fatal("phones without access")
	}
	// alice: selected, only ext1
	if err := s.SetUserAccess(ctx, alice.ID, []*Access{{PBXID: pbx.ID, Mode: AccessSelected, PhoneIDs: []int64{ext1.ID}}}); err != nil {
		t.Fatal(err)
	}
	if got := ids(must(s.UsablePhones(ctx, alice.ID))); !got[ext1.ID] || got[ext2.ID] || got[own.ID] || len(got) != 1 {
		t.Fatalf("alice: %v", got)
	}
	if _, err := s.UsablePhone(ctx, alice.ID, own.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("alice can use bob's phone")
	}
	// bob: own phone only with "any" mode
	if got := ids(must(s.UsablePhones(ctx, bob.ID))); len(got) != 0 {
		t.Fatal("bob's own phone usable without access")
	}
	s.SetUserAccess(ctx, bob.ID, []*Access{{PBXID: pbx.ID, Mode: AccessSelected}})
	if got := ids(must(s.UsablePhones(ctx, bob.ID))); got[own.ID] {
		t.Fatal("own phone usable in selected mode")
	}
	s.SetUserAccess(ctx, bob.ID, []*Access{{PBXID: pbx.ID, Mode: AccessAny, PhoneIDs: []int64{ext2.ID}}})
	if got := ids(must(s.UsablePhones(ctx, bob.ID))); !got[own.ID] || !got[ext2.ID] || got[ext1.ID] {
		t.Fatalf("bob: %v", got)
	}
	// a phone granted under the wrong PBX is refused
	if err := s.SetUserAccess(ctx, alice.ID, []*Access{{PBXID: pbx2.ID, Mode: AccessSelected, PhoneIDs: []int64{ext1.ID}}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-PBX grant: %v", err)
	}
	// user-owned phones cannot be granted to others
	if err := s.SetUserAccess(ctx, alice.ID, []*Access{{PBXID: pbx.ID, Mode: AccessSelected, PhoneIDs: []int64{own.ID}}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("granting bob's phone: %v", err)
	}
	// the failed update left alice's previous grants intact (transaction)
	if got := ids(must(s.UsablePhones(ctx, alice.ID))); !got[ext1.ID] {
		t.Fatal("failed update was not rolled back")
	}
	// disabling the PBX removes everything
	pbx.Enabled = false
	s.UpdatePBX(ctx, pbx)
	if ps := must(s.UsablePhones(ctx, bob.ID)); len(ps) != 0 {
		t.Fatal("disabled PBX still usable")
	}
	pbx.Enabled = true
	s.UpdatePBX(ctx, pbx)
	users := must(s.PhoneUsers(ctx, ext2.ID))
	if len(users) != 1 || users[0] != bob.ID {
		t.Fatalf("phone users %v", users)
	}
	// disabled users are not phone users
	d := true
	s.UpdateUser(ctx, bob.ID, UserUpdate{Disabled: &d})
	if users := must(s.PhoneUsers(ctx, ext2.ID)); len(users) != 0 {
		t.Fatal("disabled user listed")
	}
	// deleting the PBX cascades
	s.DeletePBX(ctx, pbx.ID)
	if _, err := s.GetPhone(ctx, ext1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("phone survived PBX deletion")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestLastAdmin(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	a := user(t, s, "root", true)
	f := false
	if err := s.UpdateUser(ctx, a.ID, UserUpdate{IsAdmin: &f}); !errors.Is(err, ErrLastAdmin) {
		t.Fatal("last admin demoted")
	}
	if err := s.DeleteUser(ctx, a.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatal("last admin deleted")
	}
	b := user(t, s, "second", true)
	if err := s.DeleteUser(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	tr := true
	if err := s.UpdateUser(ctx, b.ID, UserUpdate{Disabled: &tr}); !errors.Is(err, ErrLastAdmin) {
		t.Fatal("last admin disabled")
	}
	if _, err := s.CreateFirstAdmin(ctx, NewUser{Username: "x", PasswordHash: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatal("second first-admin")
	}
	if _, err := s.CreateUser(ctx, NewUser{Username: "SECOND", PasswordHash: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatal("usernames must be case-insensitive unique")
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	now := time.Unix(1_700_000_000, 0)
	s.SetClock(func() time.Time { return now })
	u := user(t, s, "u", false)
	se, err := s.CreateSession(ctx, []byte("h1"), u.ID, SessionWeb, "Firefox", "1.2.3.4", time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, []byte("h1")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(50 * time.Minute)
	got, _ := s.SessionByToken(ctx, []byte("h1"))
	s.TouchSession(ctx, got, "1.2.3.4")
	now = now.Add(50 * time.Minute) // idle 50 min since touch: still valid
	if _, err := s.SessionByToken(ctx, []byte("h1")); err != nil {
		t.Fatal("touched session expired")
	}
	now = now.Add(2 * time.Hour)
	if _, err := s.SessionByToken(ctx, []byte("h1")); !errors.Is(err, ErrNotFound) {
		t.Fatal("idle session alive")
	}
	// password change ends other sessions
	s.CreateSession(ctx, []byte("a"), u.ID, SessionWeb, "", "", time.Hour, time.Hour)
	keep, _ := s.CreateSession(ctx, []byte("b"), u.ID, SessionApp, "", "", time.Hour, time.Hour)
	s.SetPassword(ctx, u.ID, "new", false, keep.ID)
	if _, err := s.SessionByToken(ctx, []byte("a")); !errors.Is(err, ErrNotFound) {
		t.Fatal("other session survived password change")
	}
	if _, err := s.SessionByToken(ctx, []byte("b")); err != nil {
		t.Fatal("current session ended")
	}
	_ = se
}

func TestTOTPReplayAndRecovery(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	u := user(t, s, "u", false)
	s.SetTOTP(ctx, u.ID, []byte("enc"), false)
	s.EnableTOTP(ctx, u.ID, 100, nil)
	if ok, _ := s.UseTOTPStep(ctx, u.ID, 100); ok {
		t.Fatal("step replayed")
	}
	if ok, _ := s.UseTOTPStep(ctx, u.ID, 101); !ok {
		t.Fatal("next step refused")
	}
	if ok, _ := s.UseTOTPStep(ctx, u.ID, 99); ok {
		t.Fatal("older step accepted")
	}
}

func TestSettingsValidation(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t)
	st := DefaultSettings()
	st.PasswordMinLength = 4
	if err := s.SaveSettings(ctx, st); err == nil {
		t.Fatal("weak password length accepted")
	}
	st = DefaultSettings()
	st.Require2FA = "all"
	if err := s.SaveSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSettings(ctx); got.Require2FA != "all" {
		t.Fatal("not saved")
	}
}
