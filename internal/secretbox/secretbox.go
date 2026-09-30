// Package secretbox encrypts secrets at rest (SIP passwords, TOTP seeds) with AES-256-GCM.
//
// Every ciphertext is bound to a caller-chosen context string (AAD), e.g. "phone:<token>",
// so a ciphertext copied into another row or column fails to decrypt.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const version byte = 1

type Box struct {
	aead cipher.AEAD
}

// New derives the data-encryption key from a 32-byte master key.
func New(master []byte) (*Box, error) {
	if len(master) != 32 {
		return nil, errors.New("secretbox: master key must be 32 bytes")
	}
	key, err := hkdf.Key(sha256.New, master, nil, "web-ip-phone secrets v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext for the given context.
func (b *Box) Seal(plaintext []byte, context string) []byte {
	out := make([]byte, 1+b.aead.NonceSize(), 1+b.aead.NonceSize()+len(plaintext)+b.aead.Overhead())
	out[0] = version
	if _, err := rand.Read(out[1:]); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return b.aead.Seal(out, out[1:1+b.aead.NonceSize()], plaintext, []byte(context))
}

// Open decrypts a value produced by Seal with the same context.
func (b *Box) Open(ciphertext []byte, context string) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < 1+ns+b.aead.Overhead() || ciphertext[0] != version {
		return nil, errors.New("secretbox: malformed ciphertext")
	}
	pt, err := b.aead.Open(nil, ciphertext[1:1+ns], ciphertext[1+ns:], []byte(context))
	if err != nil {
		return nil, errors.New("secretbox: decryption failed (wrong key or tampered data)")
	}
	return pt, nil
}

// LoadOrCreateKey reads a base64 key file, creating it (mode 0600) with a random key when
// it does not exist.
func LoadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		key, err := base64.StdEncoding.DecodeString(string(trimSpace(b)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s: expected 32 bytes, base64 encoded", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
