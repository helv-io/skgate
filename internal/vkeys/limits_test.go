package vkeys

import (
	"strings"
	"testing"
	"time"
)

func TestKeysAreUnlimitedByDefault(t *testing.T) {
	m := New(newDB(t))
	full, k, _ := m.Create("free")
	if k.Limited() || k.LimitsText() != "unlimited" {
		t.Fatalf("%+v", k)
	}
	got, _ := m.Verify(full)
	for i := 0; i < 1000; i++ {
		if m.Check(got) != nil {
			t.Fatal("an unlimited key was rejected")
		}
	}
}

func TestRateLimitIsPerKeyPerMinute(t *testing.T) {
	m := New(newDB(t))
	now := time.Unix(1_700_000_000, 0)
	m.Now = func() time.Time { return now }
	a, ka, _ := m.Create("a")
	b, kb, _ := m.Create("b")
	if err := m.SetLimits(ka.ID, 3, time.Time{}); err != nil {
		t.Fatal(err)
	}
	va, _ := m.Verify(a)
	vb, _ := m.Verify(b)
	if va.RatePerMin != 3 || va.LimitsText() != "3/min" || vb.Limited() || kb.ID == ka.ID {
		t.Fatalf("%+v %+v", va, vb)
	}
	for i := 0; i < 3; i++ {
		if m.Check(va) != nil {
			t.Fatalf("request %d refused", i+1)
		}
	}
	now = now.Add(20 * time.Second)
	rej := m.Check(va)
	if rej == nil || rej.Code != "rate_limit_exceeded" || rej.RetryAfterSeconds() != 40 || !strings.Contains(rej.Message, "3 requests per minute") {
		t.Fatalf("%+v", rej)
	}
	if m.Check(vb) != nil {
		t.Fatal("another key must not be affected")
	}
	now = now.Add(41 * time.Second) // the window has ended
	if m.Check(va) != nil {
		t.Fatal("the next minute starts fresh")
	}
}

func TestExpiredKeysAreRefusedAndCanBeExtended(t *testing.T) {
	m := New(newDB(t))
	now := time.Unix(1_800_000_000, 0)
	m.Now = func() time.Time { return now }
	full, k, _ := m.Create("temp")
	if k.ExpiresAt != (time.Time{}) || k.Expired(now) {
		t.Fatalf("a new key never expires: %+v", k)
	}
	when := now.Add(time.Hour)
	if err := m.SetLimits(k.ID, 0, when); err != nil {
		t.Fatal(err)
	}
	v, ok := m.Verify(full)
	if !ok || !v.ExpiresAt.Equal(when) || v.Expired(now) {
		t.Fatalf("not yet: %+v %v", v, ok)
	}
	if _, late := m.ExpiredAt(full); late {
		t.Fatal("not expired yet")
	}
	now = when.Add(-time.Second)
	if _, ok := m.Verify(full); !ok {
		t.Fatal("one second before")
	}
	now = when // the moment itself is already expired
	if _, ok := m.Verify(full); ok {
		t.Fatal("an expired key verified")
	}
	if at, late := m.ExpiredAt(full); !late || !at.Equal(when) {
		t.Fatalf("%v %v", at, late)
	}
	if _, late := m.ExpiredAt("sk-unknown-key-xxxxxxxxxxxxxxxxxxxxxxxx"); late {
		t.Error("an unknown key is not an expired one")
	}
	ks, _ := m.List()
	if !ks[0].Expired(now) || !ks[0].ExpiresAt.Equal(when) {
		t.Fatalf("the list shows it: %+v", ks[0])
	}
	// moving the date brings the same secret back; clearing it ends the expiration
	if err := m.SetLimits(k.ID, 0, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Verify(full); !ok {
		t.Fatal("extended key refused")
	}
	if err := m.SetLimits(k.ID, 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(1000 * 24 * time.Hour)
	if v, ok := m.Verify(full); !ok || !v.ExpiresAt.IsZero() {
		t.Fatalf("cleared: %+v %v", v, ok)
	}
	if !strings.Contains(ExpiredMessage(when), "expired on") {
		t.Error("the message says expired")
	}
}

func TestSetLimitsValidationAndRegenerate(t *testing.T) {
	m := New(newDB(t))
	full, k, _ := m.Create("x")
	for _, bad := range []int64{-1, MaxLimit + 1} {
		if m.SetLimits(k.ID, bad, time.Time{}) == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	exp := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	m.SetLimits(k.ID, 10, exp)
	nf, nk, err := m.Regenerate(k.ID)
	if err != nil || nf == full || nk.RatePerMin != 10 || !nk.ExpiresAt.Equal(exp) {
		t.Fatalf("regenerate must keep the limits: %+v %v", nk, err)
	}
	ks, _ := m.List()
	if ks[0].RatePerMin != 10 || ks[0].LimitsText() != "10/min" || !ks[0].ExpiresAt.Equal(exp) {
		t.Fatalf("%+v", ks[0])
	}
	m.Revoke(k.ID)
	if m.SetLimits(k.ID, 1, time.Time{}) == nil {
		t.Error("a revoked key has no limits to edit")
	}
}
