// Package secrets encrypts small values (tokens, env values, headers) before they are written to
// SQLite. AES-256-GCM with a random nonce per value; the stored form is "enc:v2:" plus base64url.
//
// The key comes from SECRETS_KEY (base64 of 32 bytes, or any passphrase, which is stretched with
// scrypt) or, when
// unset, from a random key file created next to the database. A key in the environment keeps the
// database volume alone useless to an attacker; the key file only keeps secrets out of plain SQL
// dumps and backups of the database file.
//
// Values sealed by older releases ("enc:v1:", passphrases hashed with one round of SHA-256) still
// open; Legacy reports them so the store can seal them again in the current form.
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

	"golang.org/x/crypto/scrypt"
)

const (
	prefix   = "enc:v2:"
	prefixV1 = "enc:v1:"
)

// Box seals and opens values with one key. v1 opens values sealed by older releases; it differs
// from aead only when SECRETS_KEY is a passphrase.
type Box struct {
	aead, v1 cipher.AEAD
	// Passphrase is true when SECRETS_KEY is not a 32-byte base64 key.
	Passphrase bool
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("secrets: key must be 32 bytes")
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

// New builds a Box from a 32-byte key.
func New(key []byte) (*Box, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	return &Box{aead: g, v1: g}, nil
}

// FromString builds the Box for a SECRETS_KEY value (see KeyFromString).
func FromString(s string) (*Box, error) {
	key, pass := KeyFromString(s)
	b, err := New(key)
	if err != nil || !pass {
		return b, err
	}
	if b.v1, err = gcm(legacyPassphraseKey(s)); err != nil {
		return nil, err
	}
	b.Passphrase = true
	return b, nil
}

// Ephemeral returns a Box with a random key that is lost on exit (tests, or no data directory).
func Ephemeral() *Box {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	b, _ := New(k)
	return b
}

// KeyFromString turns SECRETS_KEY into a key: base64 (std or URL, padded or not) of exactly 32
// bytes is used as is, anything else is a passphrase stretched with scrypt (pass is then true).
func KeyFromString(s string) (key []byte, pass bool) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, false
		}
	}
	// N=2^15, r=8, p=1: about 32 MB and a few tens of milliseconds, once per start. The salt is
	// fixed so the same passphrase opens the same database anywhere; it only rules out tables
	// built for other software.
	k, err := scrypt.Key([]byte(s), []byte("skgate/secrets/v2"), 1<<15, 8, 1, 32)
	if err != nil {
		panic(err) // only for invalid parameters, which are constants
	}
	return k, true
}

// legacyPassphraseKey is how releases before enc:v2 turned a passphrase into a key.
func legacyPassphraseKey(s string) []byte {
	h := sha256.Sum256([]byte("skgate/secrets/v1\x00" + strings.TrimSpace(s)))
	return h[:]
}

// LoadOrCreate returns the Box for envKey (SECRETS_KEY) when set, else for the key file, which is
// created with mode 0600 when missing. The second result names the key source for logs.
func LoadOrCreate(envKey, keyFile string) (*Box, string, error) {
	if strings.TrimSpace(envKey) != "" {
		b, err := FromString(envKey)
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

// IsSealed reports whether s is in a sealed form (current or legacy).
func IsSealed(s string) bool { return strings.HasPrefix(s, prefix) || strings.HasPrefix(s, prefixV1) }

// Legacy reports whether s was sealed by an older release and should be sealed again.
func Legacy(s string) bool { return strings.HasPrefix(s, prefixV1) }

// Reseal opens a legacy value and seals it in the current form. Other values come back unchanged.
func (b *Box) Reseal(s string) (string, error) {
	if !Legacy(s) {
		return s, nil
	}
	plain, err := b.Open(s)
	if err != nil {
		return "", err
	}
	return b.Seal(plain), nil
}

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
	aead, body := b.aead, ""
	switch {
	case strings.HasPrefix(s, prefix):
		body = strings.TrimPrefix(s, prefix)
	case strings.HasPrefix(s, prefixV1):
		aead, body = b.v1, strings.TrimPrefix(s, prefixV1)
	default:
		return s, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	ns := aead.NonceSize()
	if err != nil || len(raw) < ns+aead.Overhead() {
		return "", errors.New("secrets: malformed value")
	}
	pt, err := aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("secrets: cannot decrypt (wrong SECRETS_KEY or key file?)")
	}
	return string(pt), nil
}
