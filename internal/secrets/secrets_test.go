package secrets

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	b := Ephemeral()
	for _, v := range []string{"x", "token-abc-123", strings.Repeat("é", 500), "a\nb=c"} {
		s := b.Seal(v)
		if !IsSealed(s) || (len(v) > 8 && strings.Contains(s, v)) {
			t.Fatalf("not sealed: %q", s)
		}
		if got, err := b.Open(s); err != nil || got != v {
			t.Fatalf("open: %q %v", got, err)
		}
	}
	if b.Seal("") != "" {
		t.Fatal("empty stays empty")
	}
	if a, c := b.Seal("same"), b.Seal("same"); a == c {
		t.Fatal("nonce must be random")
	}
}

func TestOpenLegacyPlaintextAndFailures(t *testing.T) {
	b := Ephemeral()
	if got, err := b.Open("legacy-plain"); err != nil || got != "legacy-plain" {
		t.Fatalf("legacy: %q %v", got, err)
	}
	s := b.Seal("secret")
	if _, err := Ephemeral().Open(s); err == nil {
		t.Fatal("wrong key must fail")
	}
	tampered := s[:len(s)-2] + "AA"
	if tampered == s {
		tampered = s[:len(s)-2] + "BB"
	}
	if _, err := b.Open(tampered); err == nil {
		t.Fatal("tampered value must fail")
	}
	if _, err := b.Open(prefix + "!!"); err == nil {
		t.Fatal("malformed value must fail")
	}
}

func TestKeyFromString(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawURLEncoding} {
		if got, pass := KeyFromString(enc.EncodeToString(raw)); string(got) != string(raw) || pass {
			t.Fatal("base64 key must be used as is")
		}
	}
	a, pass := KeyFromString("passphrase one")
	c, _ := KeyFromString("passphrase two")
	again, _ := KeyFromString("passphrase one")
	if !pass || len(a) != 32 || string(a) == string(c) || string(a) != string(again) {
		t.Fatal("passphrase must stretch deterministically to 32 bytes")
	}
	if string(a) == string(legacyPassphraseKey("passphrase one")) {
		t.Fatal("v2 passphrase key must not be the v1 hash")
	}
}

// v1sealed seals plain the way releases before enc:v2 did.
func v1sealed(t *testing.T, key []byte, plain string) string {
	g, err := gcm(key)
	if err != nil {
		t.Fatal(err)
	}
	b := &Box{aead: g, v1: g}
	return prefixV1 + strings.TrimPrefix(b.Seal(plain), prefix)
}

func TestPassphraseOpensV1AndResealsAsV2(t *testing.T) {
	b, err := FromString("correct horse battery staple")
	if err != nil || !b.Passphrase {
		t.Fatalf("passphrase box: %v %v", err, b)
	}
	old := v1sealed(t, legacyPassphraseKey("correct horse battery staple"), "tok")
	if !IsSealed(old) || !Legacy(old) {
		t.Fatal("v1 value must read as sealed legacy")
	}
	if got, err := b.Open(old); err != nil || got != "tok" {
		t.Fatalf("v1 open: %q %v", got, err)
	}
	nv, err := b.Reseal(old)
	if err != nil || !strings.HasPrefix(nv, "enc:v2:") || Legacy(nv) {
		t.Fatalf("reseal: %q %v", nv, err)
	}
	if got, _ := b.Open(nv); got != "tok" {
		t.Fatal("v2 open")
	}
	if same, _ := b.Reseal(nv); same != nv {
		t.Fatal("reseal of a v2 value must be a no-op")
	}
	// a v2 value does not open with the legacy key
	g, _ := gcm(legacyPassphraseKey("correct horse battery staple"))
	if _, err := (&Box{aead: g, v1: g}).Open(nv); err == nil {
		t.Fatal("v2 must use the scrypt key")
	}
	raw := make([]byte, 32)
	kb, _ := FromString(base64.StdEncoding.EncodeToString(raw))
	if kb.Passphrase {
		t.Fatal("base64 key is not a passphrase")
	}
	if got, err := kb.Open(v1sealed(t, raw, "x")); err != nil || got != "x" {
		t.Fatalf("raw key v1 open: %q %v", got, err)
	}
}

func TestLoadOrCreateKeyFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "secret.key")
	b1, src, err := LoadOrCreate("", f)
	if err != nil || !strings.Contains(src, "created") {
		t.Fatalf("create: %v %q", err, src)
	}
	if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v %v", fi, err)
	}
	sealed := b1.Seal("persist")
	b2, src, err := LoadOrCreate("", f)
	if err != nil || strings.Contains(src, "created") {
		t.Fatalf("reload: %v %q", err, src)
	}
	if got, err := b2.Open(sealed); err != nil || got != "persist" {
		t.Fatalf("key must persist: %q %v", got, err)
	}
	// SECRETS_KEY wins over the file
	be, src, err := LoadOrCreate("from-env", f)
	if err != nil || src != "SECRETS_KEY" {
		t.Fatalf("env: %v %q", err, src)
	}
	if _, err := be.Open(sealed); err == nil {
		t.Fatal("env key must differ from the file key")
	}
	os.WriteFile(f, []byte("not base64!!"), 0o600)
	if _, _, err := LoadOrCreate("", f); err == nil {
		t.Fatal("corrupt key file must be refused, not silently replaced")
	}
	if _, src, err := LoadOrCreate("", ""); err != nil || src != "ephemeral" {
		t.Fatalf("no file: %v %q", err, src)
	}
}
