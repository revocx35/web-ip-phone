// Package config reads the server configuration from WEBPHONE_* environment variables.
//
// Infrastructure settings (ports, TLS, proxies, keys) live here; policy settings that an
// admin may change at runtime (2FA requirement, session lifetimes, ...) live in the database.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DataDir string

	// HTTPSListen is the address of the built-in HTTPS server ("" disables it).
	HTTPSListen string
	// HTTPListen is the address of the plain-HTTP server ("" disables it). Meant for a
	// reverse proxy on the same host/LAN that terminates TLS.
	HTTPListen  string
	TLSCertFile string
	TLSKeyFile  string

	// TrustedProxies may set X-Forwarded-For / X-Forwarded-Proto.
	TrustedProxies []netip.Prefix
	// PublicOrigins are extra origins (scheme://host[:port]) accepted for browser requests
	// besides the request's own Host.
	PublicOrigins []string

	SIPPort     int
	RTPPortMin  int
	RTPPortMax  int
	AdvertiseIP net.IP // nil = auto-detect the local address towards each PBX
	SIPTrace    bool

	SecretKey  []byte // 32 bytes, nil = load/create DataDir/secret.key
	SetupToken string // "" = random token printed to the log

	MaxCalls int // global cap of simultaneous calls

	LogLevel slog.Level
}

func env(key, def string) string {
	if v, ok := os.LookupEnv("WEBPHONE_" + key); ok {
		return strings.TrimSpace(v)
	}
	return def
}

// envSecret reads WEBPHONE_<key> or the file named by WEBPHONE_<key>_FILE (Docker secrets).
func envSecret(key string) (string, error) {
	if f := env(key+"_FILE", ""); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("WEBPHONE_%s_FILE: %w", key, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return env(key, ""), nil
}

func envInt(key string, def, min, max int) (int, error) {
	s := env(key, "")
	if s == "" {
		return def, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min || v > max {
		return 0, fmt.Errorf("WEBPHONE_%s must be an integer between %d and %d", key, min, max)
	}
	return v, nil
}

func envBool(key string, def bool) (bool, error) {
	s := strings.ToLower(env(key, ""))
	switch s {
	case "":
		return def, nil
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("WEBPHONE_%s must be true or false", key)
}

// Load reads and validates the environment.
func Load() (*Config, error) {
	c := &Config{
		DataDir:     env("DATA_DIR", "/data"),
		HTTPSListen: env("HTTPS_LISTEN", ":8443"),
		HTTPListen:  env("HTTP_LISTEN", ""),
		TLSCertFile: env("TLS_CERT_FILE", ""),
		TLSKeyFile:  env("TLS_KEY_FILE", ""),
	}
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if c.HTTPSListen == "" && c.HTTPListen == "" {
		add(errors.New("both WEBPHONE_HTTPS_LISTEN and WEBPHONE_HTTP_LISTEN are empty"))
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		add(errors.New("set both WEBPHONE_TLS_CERT_FILE and WEBPHONE_TLS_KEY_FILE, or neither"))
	}

	var err error
	c.TrustedProxies, err = ParseProxies(env("TRUSTED_PROXIES", ""))
	add(err)
	for _, o := range splitList(env("PUBLIC_ORIGINS", "")) {
		o = strings.TrimRight(strings.ToLower(o), "/")
		if !strings.HasPrefix(o, "https://") && !strings.HasPrefix(o, "http://") {
			add(fmt.Errorf("WEBPHONE_PUBLIC_ORIGINS: %q must start with https:// or http://", o))
			continue
		}
		c.PublicOrigins = append(c.PublicOrigins, o)
	}

	c.SIPPort, err = envInt("SIP_PORT", 5070, 1, 65535)
	add(err)
	c.RTPPortMin, err = envInt("RTP_PORT_MIN", 20000, 1024, 65534)
	add(err)
	c.RTPPortMax, err = envInt("RTP_PORT_MAX", 20199, 1025, 65535)
	add(err)
	if err == nil && c.RTPPortMax-c.RTPPortMin < 3 {
		add(errors.New("WEBPHONE_RTP_PORT_MAX must be at least WEBPHONE_RTP_PORT_MIN+3"))
	}
	if s := env("ADVERTISE_IP", ""); s != "" {
		c.AdvertiseIP = net.ParseIP(s)
		if c.AdvertiseIP == nil {
			add(fmt.Errorf("WEBPHONE_ADVERTISE_IP: %q is not an IP address", s))
		}
	}
	c.SIPTrace, err = envBool("SIP_TRACE", false)
	add(err)
	c.MaxCalls, err = envInt("MAX_CALLS", 20, 1, 1000)
	add(err)

	if s, err := envSecret("SECRET_KEY"); err != nil {
		add(err)
	} else if s != "" {
		key, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(key) != 32 {
			add(errors.New("WEBPHONE_SECRET_KEY must be 32 bytes, base64 encoded (openssl rand -base64 32)"))
		}
		c.SecretKey = key
	}
	if s, err := envSecret("SETUP_TOKEN"); err != nil {
		add(err)
	} else {
		if s != "" && len(s) < 16 {
			add(errors.New("WEBPHONE_SETUP_TOKEN must be at least 16 characters"))
		}
		c.SetupToken = s
	}

	switch strings.ToLower(env("LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		add(errors.New("WEBPHONE_LOG_LEVEL must be debug, info, warn or error"))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseProxies parses a list of IPs/CIDRs. The keywords "private" (RFC 1918, ULA,
// loopback) and "loopback" expand to the matching ranges; "" and "none" mean no proxy.
func ParseProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, p := range splitList(s) {
		switch strings.ToLower(p) {
		case "none":
			continue
		case "loopback":
			out = append(out, netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"))
			continue
		case "private":
			for _, c := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "fc00::/7", "::1/128"} {
				out = append(out, netip.MustParsePrefix(c))
			}
			continue
		}
		if strings.Contains(p, "/") {
			pr, err := netip.ParsePrefix(p)
			if err != nil {
				return nil, fmt.Errorf("WEBPHONE_TRUSTED_PROXIES: %q: %w", p, err)
			}
			out = append(out, pr.Masked())
			continue
		}
		a, err := netip.ParseAddr(p)
		if err != nil {
			return nil, fmt.Errorf("WEBPHONE_TRUSTED_PROXIES: %q is not an IP or CIDR", p)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}
