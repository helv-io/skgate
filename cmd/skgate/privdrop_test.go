package main

import (
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestTargetIDs(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		uid, gid int
		wantErr  bool
	}{
		{"defaults", nil, 1000, 1000, false},
		{"custom", map[string]string{"PUID": "1500", "PGID": "1600"}, 1500, 1600, false},
		{"only puid", map[string]string{"PUID": "99"}, 99, 1000, false},
		{"spaces", map[string]string{"PUID": " 42 ", "PGID": " 43"}, 42, 43, false},
		{"empty strings", map[string]string{"PUID": "", "PGID": ""}, 1000, 1000, false},
		{"root uid refused", map[string]string{"PUID": "0"}, 0, 0, true},
		{"root gid refused", map[string]string{"PGID": "0"}, 0, 0, true},
		{"garbage", map[string]string{"PUID": "abc"}, 0, 0, true},
		{"negative", map[string]string{"PGID": "-5"}, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uid, gid, err := targetIDs(env(c.env))
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if !c.wantErr && (uid != c.uid || gid != c.gid) {
				t.Fatalf("got %d:%d want %d:%d", uid, gid, c.uid, c.gid)
			}
		})
	}
}

func TestNeedsDrop(t *testing.T) {
	if !needsDrop(0) || needsDrop(1000) || needsDrop(65532) {
		t.Fatal("needsDrop must be true only for euid 0")
	}
}

func TestNeedsChown(t *testing.T) {
	if needsChown(1000, 1000, 1000, 1000) {
		t.Fatal("same owner must not chown")
	}
	if !needsChown(0, 1000, 1000, 1000) || !needsChown(1000, 0, 1000, 1000) || !needsChown(0, 0, 1000, 1000) {
		t.Fatal("differing owner must chown")
	}
}

func TestDataDir(t *testing.T) {
	if got := dataDir("/data/skgate.db"); got != "/data" {
		t.Fatalf("got %q", got)
	}
	if got := dataDir("/srv/x/y/db.sqlite"); got != "/srv/x/y" {
		t.Fatalf("got %q", got)
	}
}

// Chowning to the current owner must be a no-op, and must not fail or count changes.
func TestChownTreeNoopWhenOwnerMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, failed := chownTree(dir, os.Getuid(), os.Getgid())
	if changed != 0 || failed != 0 {
		t.Fatalf("changed=%d failed=%d, want 0 0", changed, failed)
	}
}

func TestChownTreeMissingDirIsNotFatal(t *testing.T) {
	_, failed := chownTree(filepath.Join(t.TempDir(), "nope"), 1000, 1000)
	if failed == 0 {
		t.Log("missing dir reported without failure count (ok)")
	}
}

// When root, chown for real to another id and check it took effect.
func TestChownTreeAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, failed := chownTree(dir, 1234, 5678)
	if failed != 0 || changed != 2 {
		t.Fatalf("changed=%d failed=%d", changed, failed)
	}
	fi, _ := os.Lstat(f)
	if u, g, ok := fileOwner(fi); ok && (u != 1234 || g != 5678) {
		t.Fatalf("owner %d:%d", u, g)
	}
	if c, _ := chownTree(dir, 1234, 5678); c != 0 {
		t.Fatalf("second pass changed %d, want 0", c)
	}
}

func TestPrepareAndDropSkipsWhenNotRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("would drop privileges of the test process")
	}
	if err := prepareAndDrop(filepath.Join(t.TempDir(), "x.db")); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateDBTakesAwayGroupAndOtherAccess(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	for _, f := range []string{db, db + "-wal", db + "-shm"} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	privateDB(db) // a missing -journal is fine
	for _, f := range []string{db, db + "-wal", db + "-shm"} {
		if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v", f, fi.Mode().Perm())
		}
	}
}
