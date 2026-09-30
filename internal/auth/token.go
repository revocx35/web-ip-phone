package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"strings"
)

// NewToken returns a random session token (256 bits) and the SHA-256 hash that is stored.
// Only the hash is persisted, so a leaked database does not leak usable tokens.
func NewToken() (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ValidTokenFormat rejects obviously malformed tokens before touching the database.
func ValidTokenFormat(t string) bool {
	if len(t) != 43 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

var humanEnc = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

// NewHumanCode returns a random code of n groups of 4 characters from an unambiguous
// alphabet (no 0/O/1/I), e.g. "K7QM-X2PD-9HNA". Each character carries 5 bits.
func NewHumanCode(groups int) string {
	b := make([]byte, (groups*4*5+7)/8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	s := humanEnc.EncodeToString(b)[:groups*4]
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:i+4])
	}
	return strings.Join(parts, "-")
}

// NormalizeHumanCode uppercases a user-typed code and removes spaces and dashes.
func NormalizeHumanCode(s string) string {
	s = strings.ToUpper(s)
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, s)
}
