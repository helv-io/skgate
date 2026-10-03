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
	if err := m.SetLimits(ka.ID, 3, 0); err != nil {
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

func TestHardStopCountsRecordedRequests(t *testing.T) {
	m := New(newDB(t))
	full, k, _ := m.Create("capped")
	if err := m.SetLimits(k.ID, 0, 5); err != nil {
		t.Fatal(err)
	}
	v, _ := m.Verify(full)
	if v.LimitsText() != "stop at 5" || m.Check(v) != nil {
		t.Fatalf("%+v", v)
	}
	m.Record(k.ID, Usage{Requests: 3})
	if m.Check(v) != nil { // pending increments count too
		t.Fatal("3 of 5 used")
	}
	m.Record(k.ID, Usage{Requests: 2})
	rej := m.Check(v)
	if rej == nil || rej.Code != "key_hard_stop" || rej.RetryAfter != 0 {
		t.Fatalf("%+v", rej)
	}
	// once written, Verify carries the stored total
	if err := m.FlushUsage(); err != nil {
		t.Fatal(err)
	}
	v, _ = m.Verify(full)
	if m.Check(v) == nil {
		t.Fatal("still stopped after the flush")
	}
	// raising the stop lifts it; clearing it too
	m.SetLimits(k.ID, 0, 6)
	v, _ = m.Verify(full)
	if m.Check(v) != nil {
		t.Fatal("raised stop must let the key through")
	}
	m.SetLimits(k.ID, 0, 0)
	v, _ = m.Verify(full)
	if v.Limited() || m.Check(v) != nil {
		t.Fatal("cleared limits")
	}
}

func TestSetLimitsValidationAndRegenerate(t *testing.T) {
	m := New(newDB(t))
	full, k, _ := m.Create("x")
	for _, bad := range [][2]int64{{-1, 0}, {0, -1}, {MaxLimit + 1, 0}} {
		if m.SetLimits(k.ID, bad[0], bad[1]) == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	m.SetLimits(k.ID, 10, 100)
	nf, nk, err := m.Regenerate(k.ID)
	if err != nil || nf == full || nk.RatePerMin != 10 || nk.HardStop != 100 {
		t.Fatalf("regenerate must keep the limits: %+v %v", nk, err)
	}
	ks, _ := m.List()
	if ks[0].RatePerMin != 10 || ks[0].HardStop != 100 || ks[0].LimitsText() != "10/min, stop at 100" {
		t.Fatalf("%+v", ks[0])
	}
	m.Revoke(k.ID)
	if m.SetLimits(k.ID, 1, 1) == nil {
		t.Error("a revoked key has no limits to edit")
	}
}
