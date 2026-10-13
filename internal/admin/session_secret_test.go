package admin

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/helv-io/skgate/internal/secrets"
	"github.com/helv-io/skgate/internal/store"
)

// The cookie HMAC key is sealed at rest (review SEC-03): the database alone cannot mint admin cookies, a
// plaintext key from an older release is sealed and kept (nobody is signed out), and a key that cannot be opened
// with the current SECRETS_KEY is replaced.
func TestSessionSecretIsSealed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skgate.db")
	db, err := store.OpenWith(path, "a passphrase")
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Repeat([]byte{7}, 48)
	if err := db.SetSetting("session_secret", base64.RawStdEncoding.EncodeToString(legacy)); err != nil {
		t.Fatal(err)
	}
	a := &Admin{DB: db}
	if got := a.secret(); !bytes.Equal(got, legacy) {
		t.Fatal("a plaintext secret from an older release must be kept")
	}
	raw, _ := db.GetSetting("session_secret")
	if !secrets.IsSealed(raw) {
		t.Fatalf("session_secret is stored as plaintext: %q", raw)
	}
	if got := (&Admin{DB: db}).secret(); !bytes.Equal(got, legacy) {
		t.Fatal("the sealed secret must read back the same")
	}
	db.Close()

	other, err := store.OpenWith(path, "another passphrase")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if got := (&Admin{DB: other}).secret(); bytes.Equal(got, legacy) || len(got) < 32 {
		t.Fatal("a secret sealed with another key must be replaced")
	}
	raw, _ = other.GetSetting("session_secret")
	if !secrets.IsSealed(raw) {
		t.Fatal("a new secret must be stored sealed")
	}
}
