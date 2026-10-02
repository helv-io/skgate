// Package secrets encrypts small values (tokens, env values, headers) before they are written to
// SQLite. AES-256-GCM with a random nonce per value; the stored form is "enc:v1:" plus base64url.
//
// The key comes from SECRETS_KEY (base64 of 32 bytes, or any passphrase, which is hashed) or, when
// unset, from a random key file created next to the database. A key in the environment keeps the
// database volume alone useless to an attacker; the key file only keeps secrets out of plain SQL
// dumps and backups of the database file.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const prefix = "enc:v1:"

// Box seals and opens values with one key.
type Box struct{ aead cipher.AEAD }

// New builds a Box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secrets: key must be 32 bytes")
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Box{aead: g}, nil
}

// Ephemeral returns a Box with a random key that is lost on exit (tests, or no data directory).
func Ephemeral() *Box {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	b, _ := New(k)
	return b
}

// KeyFromString turns SECRETS_KEY into a key: base64 (std or URL, padded or not) of exactly 32
// bytes is used as is, anything else is a passphrase hashed with SHA-256.
func KeyFromString(s string) []byte {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b
		}
	}
	h := sha256.Sum256([]byte("skgate/secrets/v1\x00" + s))
	return h[:]
}

// LoadOrCreate returns the Box for envKey (SECRETS_KEY) when set, else for the key file, which is
// created with mode 0600 when missing. The second result names the key source for logs.
func LoadOrCreate(envKey, keyFile string) (*Box, string, error) {
	if strings.TrimSpace(envKey) != "" {
		b, err := New(KeyFromString(envKey))
		return b, "SECRETS_KEY", err
	}
	if keyFile == "" {
		return Ephemeral(), "ephemeral", nil
	}
	raw, err := os.ReadFile(keyFile)
	if err == nil {
		k, derr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(k) != 32 {
			return nil, "", fmt.Errorf("secrets: %s is not a valid key file", keyFile)
		}
		b, err := New(k)
		return b, keyFile, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) { // lost a race with another process: use its key
			return LoadOrCreate("", keyFile)
		}
		return nil, "", err
	}
	_, werr := f.WriteString(base64.RawStdEncoding.EncodeToString(k) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, "", werr
	}
	b, err := New(k)
	return b, keyFile + " (created)", err
}

// IsSealed reports whether s is in the sealed form.
func IsSealed(s string) bool { return strings.HasPrefix(s, prefix) }

// Seal encrypts plain. The empty string stays empty.
func (b *Box) Seal(plain string) string {
	if plain == "" {
		return ""
	}
	nonce := make([]byte, b.aead.NonceSize())
	_, _ = rand.Read(nonce)
	ct := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawURLEncoding.EncodeToString(ct)
}

// Open decrypts a sealed value. A value without the prefix is legacy plaintext and is returned
// unchanged. A sealed value that fails authentication (wrong key, tampering) is an error.
func (b *Box) Open(s string) (string, error) {
	if !IsSealed(s) {
		return s, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	ns := b.aead.NonceSize()
	if err != nil || len(raw) < ns+b.aead.Overhead() {
		return "", errors.New("secrets: malformed value")
	}
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("secrets: cannot decrypt (wrong SECRETS_KEY or key file?)")
	}
	return string(pt), nil
}
