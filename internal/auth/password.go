// Package auth implements password hashing, session tokens, TOTP and login throttling.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP 2024 recommendation: m=46 MiB, t=1, p=1, or equivalent).
// Hashes store their parameters, so raising them later upgrades hashes at the next login.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
}

var DefaultParams = Params{Memory: 64 * 1024, Time: 2, Threads: 1}

// hashSem limits concurrent hashing so a burst of logins cannot exhaust memory.
var hashSem = make(chan struct{}, 2)

const (
	saltLen = 16
	keyLen  = 32
	// MaxPasswordLen bounds hashing cost and request size.
	MaxPasswordLen = 256
)

func derive(ctx context.Context, pw, salt []byte, p Params) ([]byte, error) {
	select {
	case hashSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSem }()
	return argon2.IDKey(pw, salt, p.Time, p.Memory, p.Threads, keyLen), nil
}

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(ctx context.Context, password string, p Params) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := derive(ctx, []byte(password), salt, p)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("malformed password hash")

func parseHash(h string) (Params, []byte, []byte, error) {
	var p Params
	parts := strings.Split(h, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, errBadHash
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return p, nil, nil, errBadHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, nil, nil, errBadHash
	}
	if p.Memory < 8*1024 || p.Memory > 1024*1024 || p.Time < 1 || p.Time > 20 || p.Threads < 1 {
		return p, nil, nil, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, errBadHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return p, nil, nil, errBadHash
	}
	return p, salt, key, nil
}

// VerifyPassword checks a password in constant time. needsRehash reports that the hash
// uses weaker parameters than p.
func VerifyPassword(ctx context.Context, password, hash string, p Params) (ok, needsRehash bool, err error) {
	hp, salt, key, err := parseHash(hash)
	if err != nil {
		return false, false, err
	}
	got, err := derive(ctx, []byte(password), salt, hp)
	if err != nil {
		return false, false, err
	}
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	return true, hp.Memory < p.Memory || hp.Time < p.Time, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// BurnPasswordCheck spends the same time as a real verification; used for unknown users so
// response timing does not reveal which usernames exist.
func BurnPasswordCheck(ctx context.Context, password string, p Params) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword(context.Background(), "dummy password for timing", p) })
	VerifyPassword(ctx, password, dummyHash, p)
}

// CheckPasswordPolicy validates a new password.
func CheckPasswordPolicy(password, username string, minLen int) error {
	n := utf8.RuneCountInString(password)
	if n < minLen {
		return fmt.Errorf("password must be at least %d characters", minLen)
	}
	if len(password) > MaxPasswordLen {
		return fmt.Errorf("password must be at most %d bytes", MaxPasswordLen)
	}
	if !utf8.ValidString(password) {
		return errors.New("password is not valid UTF-8")
	}
	for _, r := range password {
		if unicode.IsControl(r) {
			return errors.New("password must not contain control characters")
		}
	}
	lower := strings.ToLower(password)
	if username != "" && strings.Contains(lower, strings.ToLower(username)) {
		return errors.New("password must not contain the username")
	}
	if isCommonPassword(lower) {
		return errors.New("this password is too common")
	}
	distinct := map[rune]bool{}
	for _, r := range password {
		distinct[r] = true
	}
	if len(distinct) < 5 {
		return errors.New("password has too few different characters")
	}
	return nil
}

// A short list of the most common passwords that pass the length rule.
var commonPasswords = map[string]bool{}

func init() {
	for _, p := range strings.Fields(`1234567890 12345678910 123456789a 1234567890a qwertyuiop 1q2w3e4r5t 1qaz2wsx3edc
		password1 password12 password123 password1234 passw0rd123 iloveyou123 qwerty1234 qwerty12345 qwerty123456
		abcdefghij abc1234567 0987654321 1111111111 0000000000 a1b2c3d4e5 welcome123 administrator admin12345
		admin123456 letmein123 changeme123 football123 baseball123 princess123 sunshine123 superman123 1234qwerty
		zaq12wsxcde q1w2e3r4t5 asdfghjkl1 zxcvbnm123 trustno1234 michael123 password! p@ssw0rd123 password2024
		password2025 password2026 welcome2026 summer2026 winter2026`) {
		commonPasswords[p] = true
	}
}

func isCommonPassword(lower string) bool { return commonPasswords[lower] }
