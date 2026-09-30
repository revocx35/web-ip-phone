// Package tlscert provides the HTTPS certificate: a user-supplied one (reloaded when the
// files change, e.g. after a Let's Encrypt renewal) or a generated self-signed one.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Fingerprint returns the SHA-256 fingerprint of a certificate as colon-separated hex.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// Provider serves the current certificate to the TLS stack.
type Provider struct {
	certFile, keyFile string
	mu                sync.Mutex
	cert              *tls.Certificate
	modTime           time.Time
	checked           time.Time
}

// FromFiles loads a certificate/key pair and reloads it when the certificate file changes.
func FromFiles(certFile, keyFile string) (*Provider, error) {
	p := &Provider{certFile: certFile, keyFile: keyFile}
	if err := p.load(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) load() error {
	st, err := os.Stat(p.certFile)
	if err != nil {
		return err
	}
	c, err := tls.LoadX509KeyPair(p.certFile, p.keyFile)
	if err != nil {
		return err
	}
	p.cert, p.modTime = &c, st.ModTime()
	return nil
}

func (p *Provider) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.certFile != "" && time.Since(p.checked) > time.Minute {
		p.checked = time.Now()
		if st, err := os.Stat(p.certFile); err == nil && !st.ModTime().Equal(p.modTime) {
			if err := p.load(); err != nil {
				// keep serving the old certificate
				fmt.Fprintf(os.Stderr, "tls: reload %s failed: %v\n", p.certFile, err)
			}
		}
	}
	return p.cert, nil
}

// Fingerprint of the current leaf certificate.
func (p *Provider) Fingerprint() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cert == nil || len(p.cert.Certificate) == 0 {
		return ""
	}
	return Fingerprint(p.cert.Certificate[0])
}

// SelfSigned loads dir/cert.pem + dir/key.pem, creating them if missing.
func SelfSigned(dir string) (*Provider, error) {
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if _, err := os.Stat(certFile); errors.Is(err, os.ErrNotExist) {
		if err := generate(dir, certFile, keyFile); err != nil {
			return nil, err
		}
	}
	return FromFiles(certFile, keyFile)
}

func generate(dir, certFile, keyFile string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Web IP Phone", Organization: []string{"Web IP Phone (self-signed)"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	if host != "" && host != "localhost" {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ipn.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})); err != nil {
		return err
	}
	return writeFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
