package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenUsesWAL(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.JournalMode != "wal" || len(db.Warnings) != 0 {
		t.Fatalf("mode %q warnings %v", db.JournalMode, db.Warnings)
	}
}

func TestOpenFallsBackToTruncateWhenWALFails(t *testing.T) {
	journalOverride = func(mode string) error {
		if mode == "WAL" {
			return errors.New("disk I/O error")
		}
		return nil
	}
	defer func() { journalOverride = nil }()
	db, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&got); err != nil || got != "truncate" {
		t.Fatalf("journal_mode %q %v", got, err)
	}
	if db.JournalMode != "truncate" || len(db.Warnings) != 1 {
		t.Fatalf("mode %q warnings %v", db.JournalMode, db.Warnings)
	}
	db.SetSetting("a", "b")
	if v, _ := db.GetSetting("a"); v != "b" {
		t.Fatal("write failed")
	}
}

func TestOpenResealsV1ValuesAndWarnsOnPassphrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	db, err := OpenWith(path, "a passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Warnings) != 1 || !strings.Contains(db.Warnings[0], "32-byte") {
		t.Fatalf("warnings %v", db.Warnings)
	}
	// Plant v1 values the way an older release wrote them.
	g := sha256.Sum256([]byte("skgate/secrets/v1\x00a passphrase"))
	blk, _ := aes.NewCipher(g[:])
	aead, _ := cipher.NewGCM(blk)
	nonce := make([]byte, aead.NonceSize())
	v1 := "enc:v1:" + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte("secret"), nil))
	db.SetSetting("provider.x.key", v1)
	db.Exec(`INSERT INTO upstreams(alias,url,auth_value,created_at) VALUES('u','http://x',?,0)`, v1)
	db.Close()

	db, err = OpenWith(path, "a passphrase")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, _ := db.GetSetting("provider.x.key")
	var up string
	db.QueryRow(`SELECT auth_value FROM upstreams WHERE alias='u'`).Scan(&up)
	for _, v := range []string{raw, up} {
		if !strings.HasPrefix(v, "enc:v2:") {
			t.Fatalf("not resealed: %q", v)
		}
		if got, err := db.Secrets.Open(v); err != nil || got != "secret" {
			t.Fatalf("open: %q %v", got, err)
		}
	}
}

func TestBase64KeyHasNoWarning(t *testing.T) {
	db, err := OpenWith(filepath.Join(t.TempDir(), "s.db"), base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(db.Warnings) != 0 {
		t.Fatalf("warnings %v", db.Warnings)
	}
}
