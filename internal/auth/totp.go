package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

// TOTP implements RFC 6238 with the parameters every authenticator app supports:
// SHA-1, 6 digits, 30-second steps.
const (
	totpDigits = 6
	totpPeriod = 30
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns 20 random bytes (160 bits, as recommended by RFC 4226).
func NewTOTPSecret() []byte {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func TOTPSecretString(secret []byte) string { return b32.EncodeToString(secret) }

// TOTPURI returns the otpauth:// URI encoded in enrollment QR codes.
func TOTPURI(secret []byte, issuer, account string) string {
	v := url.Values{}
	v.Set("secret", TOTPSecretString(secret))
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func hotp(secret []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, code%1_000_000)
}

// TOTPCode returns the code for time t (tests and enrollment display).
func TOTPCode(secret []byte, t time.Time) string {
	return hotp(secret, uint64(t.Unix()/totpPeriod))
}

// CheckTOTP validates a code with ±1 step of clock skew and returns the matched step.
// Callers must reject steps not newer than the last accepted one (replay protection).
func CheckTOTP(secret []byte, code string, now time.Time) (step int64, ok bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	matched := int64(-1)
	for _, d := range []int64{-1, 0, 1} {
		s := cur + d
		if subtle.ConstantTimeCompare([]byte(hotp(secret, uint64(s))), []byte(code)) == 1 {
			matched = s
		}
	}
	return matched, matched >= 0
}
