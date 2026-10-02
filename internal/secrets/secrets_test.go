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
		if got := KeyFromString(enc.EncodeToString(raw)); string(got) != string(raw) {
			t.Fatal("base64 key must be used as is")
		}
	}
	a, c := KeyFromString("passphrase one"), KeyFromString("passphrase two")
	if len(a) != 32 || string(a) == string(c) || string(a) != string(KeyFromString("passphrase one")) {
		t.Fatal("passphrase must hash deterministically to 32 bytes")
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
