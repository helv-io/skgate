package store

import (
	"errors"
	"path/filepath"
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
