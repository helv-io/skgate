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

// The key is sealed when the database opens, not at the first admin request, and the cookies signed with the
// plaintext key before the upgrade still verify afterwards, so no admin is signed out by the migration.
func TestSessionSecretIsSealedAtOpenAndKeepsCookiesValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skgate.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Repeat([]byte{9}, 48)
	if err := db.SetSetting(store.SessionSecretSetting, base64.RawStdEncoding.EncodeToString(legacy)); err != nil {
		t.Fatal(err)
	}
	before := (&Admin{DB: db}).sign("msg") // signed by the key as an older release stored it
	db.Close()

	db, err = store.Open(path) // the upgrade: no admin request has happened yet
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if raw, _ := db.GetSetting(store.SessionSecretSetting); !secrets.IsSealed(raw) {
		t.Fatalf("session_secret is still plaintext after open: %q", raw)
	}
	if got := (&Admin{DB: db}).sign("msg"); got != before {
		t.Fatal("sealing the key must not change it: admins would be signed out")
	}
	// the Grok token list is read by position and must not have grown
	for _, k := range store.SealedSettings {
		if k == store.SessionSecretSetting {
			t.Fatal("session_secret must stay out of SealedSettings")
		}
	}
}
