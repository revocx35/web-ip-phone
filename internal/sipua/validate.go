package sipua

import (
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// sipgo does not escape or validate header values, so every value that reaches a SIP
// message from user input must pass these checks first (header injection).

var (
	userRe     = regexp.MustCompile(`^[0-9A-Za-z*+._~-]{1,64}$`)
	authUserRe = regexp.MustCompile(`^[0-9A-Za-z*+._~@-]{1,128}$`)
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	dialRe     = regexp.MustCompile(`^[0-9A-Za-z*#+._-]{1,64}$`)
)

// ValidUser checks a SIP user / auth user name.
func ValidUser(s string) bool { return userRe.MatchString(s) }

// ValidAuthUser checks a digest username (may be user@domain for some providers).
func ValidAuthUser(s string) bool { return authUserRe.MatchString(s) }

// ValidDialString checks a number/URI user the client wants to call.
func ValidDialString(s string) bool { return dialRe.MatchString(s) }

// ValidHost checks a host name or IP literal.
func ValidHost(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	return len(s) <= 253 && hostRe.MatchString(s)
}

// ValidPassword checks a SIP password. It only enters the digest hash, never a header, so
// any text without control characters is fine.
func ValidPassword(s string) bool {
	if s == "" || len(s) > 128 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ValidDisplayName checks a caller-ID name: printable text without quotes or backslashes.
func ValidDisplayName(s string) bool {
	if len(s) > 64 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '"' || r == '\\' || unicode.IsControl(r) || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// cleanRemoteText makes a PBX-supplied display name/user safe to show: bounded length,
// no control characters.
func cleanRemoteText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	if len(s) > 80 {
		s = s[:80]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return strings.TrimSpace(s)
}
