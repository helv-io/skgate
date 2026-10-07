package vkeys

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
	"github.com/helv-io/skgate/internal/store"
)

func newDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestKeyFormatHashOnlyVerifyRevoke(t *testing.T) {
	db := newDB(t)
	m := New(db)
	full, k, err := m.Create("my assistant")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(full, "sk-") || len(full) != 3+48 {
		t.Fatalf("bad key format: len=%d", len(full))
	}
	if !LooksLikeKey(full) {
		t.Fatal("LooksLikeKey false for minted key")
	}
	if k.Prefix != full[:8] || k.Label != "my assistant" {
		t.Fatalf("bad metadata %+v", k)
	}
	// only the hash is stored: the secret must not appear in any column
	var hash, prefix, label string
	if err := db.QueryRow(`SELECT hash,prefix,label FROM vkeys WHERE id=?`, k.ID).Scan(&hash, &prefix, &label); err != nil {
		t.Fatal(err)
	}
	if hash != httputil.SHA256Hex(full) {
		t.Fatal("stored hash mismatch")
	}
	for _, col := range []string{hash, prefix, label} {
		if strings.Contains(col, full[8:]) {
			t.Fatal("secret leaked into storage")
		}
	}
	if _, ok := m.Verify(full); !ok {
		t.Fatal("verify failed for valid key")
	}
	if _, ok := m.Verify(full[:len(full)-1] + "x"); ok && full[len(full)-1] != 'x' {
		t.Fatal("verify accepted a tampered key")
	}
	if _, ok := m.Verify("sk-short"); ok {
		t.Fatal("verify accepted junk")
	}
	if err := m.Revoke(k.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Verify(full); ok {
		t.Fatal("revoked key still verifies")
	}
	if err := m.Revoke(k.ID); err == nil {
		t.Fatal("double revoke should error")
	}
	l, _ := m.List()
	if len(l) != 1 || !l[0].Revoked {
		t.Fatalf("list wrong: %+v", l)
	}
}

func TestKeysUnique(t *testing.T) {
	m := New(newDB(t))
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		k, _, err := m.Create("x")
		if err != nil || seen[k] {
			t.Fatal("duplicate or error")
		}
		seen[k] = true
	}
}

func TestKeyMaskedShowsOnlyLast4(t *testing.T) {
	m := New(newDB(t))
	full, k, err := m.Create("x")
	if err != nil {
		t.Fatal(err)
	}
	want := "************" + full[len(full)-4:]
	if k.Masked() != want {
		t.Fatalf("masked %q want %q", k.Masked(), want)
	}
	ks, _ := m.List()
	if ks[0].Masked() != want || strings.Contains(ks[0].Masked(), full[3:len(full)-4]) {
		t.Fatalf("list masked %q", ks[0].Masked())
	}
	v, ok := m.Verify(full)
	if !ok || v.Masked() != want {
		t.Fatalf("verify masked %q", v.Masked())
	}
	if (Key{}).Masked() != "************" {
		t.Fatal("legacy key must show asterisks only")
	}
}

func setTimes(t *testing.T, m *Manager, id int64, lastUsed, revokedAt int64) {
	t.Helper()
	if _, err := m.db.Exec(`UPDATE vkeys SET last_used=?, revoked_at=? WHERE id=?`, lastUsed, revokedAt, id); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeDeletesNeverUsedKeyImmediately(t *testing.T) {
	m := New(newDB(t))
	var logs []string
	m.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	unused, uk, _ := m.Create("unused")
	used, usedK, _ := m.Create("used")
	if _, ok := m.Verify(used); !ok {
		t.Fatal("verify")
	}
	if err := m.Revoke(uk.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Revoke(usedK.ID); err != nil {
		t.Fatal(err)
	}
	l, _ := m.List()
	if len(l) != 1 || l[0].ID != usedK.ID || !l[0].Revoked {
		t.Fatalf("never-used revoked key must be gone, used one kept as revoked: %+v", l)
	}
	if _, ok := m.Verify(unused); ok {
		t.Fatal("deleted key verifies")
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "purged") || !strings.Contains(logs[0], `"unused"`) || !strings.Contains(logs[0], "never used") {
		t.Fatalf("purge not logged: %q", logs)
	}
	if strings.Contains(logs[0], unused) {
		t.Fatal("log leaked the secret")
	}
}

func TestPurgeRevokedRetention(t *testing.T) {
	m := New(newDB(t))
	m.Logf = func(string, ...any) {}
	now := time.Now()
	mk := func(label string, lastUsed time.Time, revoked bool) int64 {
		_, k, _ := m.Create(label)
		var lu, rv int64
		if !lastUsed.IsZero() {
			lu = lastUsed.Unix()
		}
		if revoked {
			rv = now.Unix()
		}
		setTimes(t, m, k.ID, lu, rv)
		return k.ID
	}
	old := mk("old-revoked", now.Add(-31*24*time.Hour), true)
	edge := mk("recent-revoked", now.Add(-29*24*time.Hour), true)
	never := mk("never-revoked", time.Time{}, true)
	oldActive := mk("old-active", now.Add(-90*24*time.Hour), false)
	neverActive := mk("never-active", time.Time{}, false)
	gone, err := m.PurgeRevoked(now)
	if err != nil || len(gone) != 2 {
		t.Fatalf("purged %d, %v", len(gone), err)
	}
	have := map[int64]bool{}
	l, _ := m.List()
	for _, k := range l {
		have[k.ID] = true
	}
	if have[old] || have[never] || !have[edge] || !have[oldActive] || !have[neverActive] {
		t.Fatalf("wrong survivors: %v", have)
	}
	// 30 days count from the last use, not from the revocation
	if gone, _ = m.PurgeRevoked(now.Add(2 * 24 * time.Hour)); len(gone) != 1 || gone[0].ID != edge {
		t.Fatalf("recent-revoked should expire 30 days after last use: %+v", gone)
	}
	if gone, _ = m.PurgeRevoked(now.Add(400 * 24 * time.Hour)); len(gone) != 0 {
		t.Fatalf("active keys must never be purged: %+v", gone)
	}
}

func TestRunPurgeSweepsAtStartup(t *testing.T) {
	m := New(newDB(t))
	m.Logf = func(string, ...any) {}
	_, k, _ := m.Create("stale")
	setTimes(t, m, k.ID, time.Now().Add(-40*24*time.Hour).Unix(), time.Now().Unix())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.RunPurge(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l, _ := m.List(); len(l) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if l, _ := m.List(); len(l) != 0 {
		t.Fatalf("startup sweep did not purge: %+v", l)
	}
}

func TestRegenerateReplacesSecretKeepsRecord(t *testing.T) {
	m := New(newDB(t))
	old, k, _ := m.Create("svc")
	if _, ok := m.Verify(old); !ok {
		t.Fatal("verify")
	}
	before, _ := m.List()
	fresh, nk, err := m.Regenerate(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old || !LooksLikeKey(fresh) {
		t.Fatalf("bad new secret %q", fresh)
	}
	if _, ok := m.Verify(old); ok {
		t.Fatal("old token must stop working immediately")
	}
	if got, ok := m.Verify(fresh); !ok || got.ID != k.ID {
		t.Fatal("new token must verify as the same record")
	}
	after, _ := m.List()
	if len(after) != 1 || after[0].ID != k.ID || after[0].Label != "svc" || !after[0].CreatedAt.Equal(before[0].CreatedAt) || !after[0].LastUsed.Equal(before[0].LastUsed) {
		t.Fatalf("record changed: %+v -> %+v", before, after)
	}
	if after[0].Last4 != fresh[len(fresh)-4:] || nk.Last4 != after[0].Last4 || after[0].Prefix != fresh[:8] {
		t.Fatalf("display fields not updated: %+v", after[0])
	}
	if _, _, err := m.Regenerate(9999); err == nil {
		t.Fatal("unknown id must error")
	}
	m.Logf = func(string, ...any) {}
	m.Revoke(k.ID)
	if _, _, err := m.Regenerate(k.ID); err == nil {
		t.Fatal("revoked key must not regenerate")
	}
}
