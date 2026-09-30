package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

var fast = Params{Memory: 8 * 1024, Time: 1, Threads: 1}

func TestPasswordHash(t *testing.T) {
	ctx := context.Background()
	h, err := HashPassword(ctx, "correct horse battery", fast)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("format: %s", h)
	}
	ok, rehash, err := VerifyPassword(ctx, "correct horse battery", h, fast)
	if !ok || rehash || err != nil {
		t.Fatalf("verify: %v %v %v", ok, rehash, err)
	}
	if ok, _, _ := VerifyPassword(ctx, "wrong", h, fast); ok {
		t.Fatal("wrong password accepted")
	}
	// stronger parameters request a rehash
	if _, rehash, _ := VerifyPassword(ctx, "correct horse battery", h, Params{Memory: 16 * 1024, Time: 1, Threads: 1}); !rehash {
		t.Fatal("no rehash for weaker hash")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=8192,t=1,p=1$AAAA$AAAA", "$argon2id$v=19$m=1,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA"} {
		if ok, _, err := VerifyPassword(ctx, "x", bad, fast); ok || err == nil {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	for pw, ok := range map[string]bool{
		"short":                 false,
		"password123":           false, // common
		"aaaaaaaaaaaaaaa":       false, // too few distinct characters
		"alice-in-wonderland":   false, // contains username
		"Tr0ub4dor&3-horse":     true,
		"çok güzel bir parola!": true,
	} {
		err := CheckPasswordPolicy(pw, "alice", 10)
		if (err == nil) != ok {
			t.Errorf("%q: %v", pw, err)
		}
	}
	if CheckPasswordPolicy("with\x00control-char", "", 10) == nil {
		t.Error("control character accepted")
	}
}

// RFC 6238 appendix B test vectors (SHA-1, 8 digits there; 6 digits = the last 6).
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := TOTPCode(secret, time.Unix(ts, 0)); got != want {
			t.Errorf("t=%d: got %s want %s", ts, got, want)
		}
	}
	now := time.Unix(1234567890, 0)
	if step, ok := CheckTOTP(secret, "005924", now); !ok || step != 1234567890/30 {
		t.Fatal("current code rejected")
	}
	if _, ok := CheckTOTP(secret, TOTPCode(secret, now.Add(-30*time.Second)), now); !ok {
		t.Fatal("previous step rejected (clock skew)")
	}
	if _, ok := CheckTOTP(secret, TOTPCode(secret, now.Add(-90*time.Second)), now); ok {
		t.Fatal("old code accepted")
	}
	if _, ok := CheckTOTP(secret, "12345", now); ok {
		t.Fatal("short code accepted")
	}
}

func TestThrottle(t *testing.T) {
	now := time.Unix(1000, 0)
	th := NewThrottle(3, 2*time.Second, time.Minute, time.Hour)
	th.SetClock(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if th.Wait("ip:a", "user:x") > 0 {
			t.Fatal("throttled too early")
		}
		th.Fail("ip:a", "user:x")
	}
	if th.Wait("user:x") != 0 {
		t.Fatal("3 free failures")
	}
	th.Fail("ip:a", "user:x")
	if w := th.Wait("ip:b", "user:x"); w != 2*time.Second {
		t.Fatalf("wait %v", w)
	}
	th.Fail("ip:a", "user:x")
	if w := th.Wait("user:x"); w != 4*time.Second {
		t.Fatalf("doubling: %v", w)
	}
	for i := 0; i < 20; i++ {
		th.Fail("user:x")
	}
	if w := th.Wait("user:x"); w != time.Minute {
		t.Fatalf("cap: %v", w)
	}
	now = now.Add(2 * time.Hour)
	if th.Wait("user:x", "ip:a") != 0 {
		t.Fatal("not forgotten after window")
	}
	th.Fail("user:y")
	th.Reset("user:y")
	if th.Wait("user:y") != 0 {
		t.Fatal("reset")
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	rl := NewRateLimiterPer(10, time.Hour, 3)
	rl.SetClock(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if !rl.Allow("u") {
			t.Fatal("burst")
		}
	}
	if rl.Allow("u") {
		t.Fatal("over burst")
	}
	now = now.Add(6 * time.Minute) // one token per 6 minutes
	if !rl.Allow("u") || rl.Allow("u") {
		t.Fatal("refill")
	}
}

func TestTokens(t *testing.T) {
	tok, hash := NewToken()
	if !ValidTokenFormat(tok) || len(hash) != 32 || string(HashToken(tok)) != string(hash) {
		t.Fatal("token")
	}
	if ValidTokenFormat(tok+"x") || ValidTokenFormat(strings.Repeat("!", 43)) {
		t.Fatal("bad token accepted")
	}
	c := NewHumanCode(5)
	if len(c) != 24 || NormalizeHumanCode(strings.ToLower(c)) != strings.ReplaceAll(c, "-", "") {
		t.Fatalf("human code %q", c)
	}
}
