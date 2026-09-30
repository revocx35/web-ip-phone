package config

import (
	"net/netip"
	"testing"
)

func TestParseProxies(t *testing.T) {
	ps, err := ParseProxies("private, 203.0.113.7 2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	contains := func(s string) bool {
		a := netip.MustParseAddr(s)
		for _, p := range ps {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	for addr, want := range map[string]bool{"10.1.2.3": true, "192.168.68.5": true, "172.20.0.1": true, "127.0.0.1": true,
		"203.0.113.7": true, "203.0.113.8": false, "8.8.8.8": false, "2001:db8::1": true, "fd00::1": true} {
		if contains(addr) != want {
			t.Errorf("%s: want %v", addr, want)
		}
	}
	if ps, _ := ParseProxies("none"); len(ps) != 0 {
		t.Error("none")
	}
	if _, err := ParseProxies("not-an-ip"); err == nil {
		t.Error("garbage accepted")
	}
}

func TestLoadValidates(t *testing.T) {
	t.Setenv("WEBPHONE_RTP_PORT_MIN", "30000")
	t.Setenv("WEBPHONE_RTP_PORT_MAX", "30001")
	if _, err := Load(); err == nil {
		t.Error("tiny RTP range accepted")
	}
	t.Setenv("WEBPHONE_RTP_PORT_MAX", "30100")
	t.Setenv("WEBPHONE_SECRET_KEY", "short")
	if _, err := Load(); err == nil {
		t.Error("short secret key accepted")
	}
	t.Setenv("WEBPHONE_SECRET_KEY", "")
	t.Setenv("WEBPHONE_SETUP_TOKEN", "abc")
	if _, err := Load(); err == nil {
		t.Error("short setup token accepted")
	}
	t.Setenv("WEBPHONE_SETUP_TOKEN", "")
	c, err := Load()
	if err != nil || c.HTTPSListen != ":8443" || c.SIPPort != 5070 {
		t.Fatalf("%v %+v", err, c)
	}
}
