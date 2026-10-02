package vkeys

import (
	"sync"
	"testing"
	"time"
)

func usageOf(t *testing.T, m *Manager, id int64) Usage {
	t.Helper()
	ks, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range ks {
		if k.ID == id {
			return k.Usage
		}
	}
	t.Fatalf("key %d not listed", id)
	return Usage{}
}

func TestUsageAccumulatesAcrossFlushes(t *testing.T) {
	m := New(newDB(t))
	_, k, _ := m.Create("a")
	if u := usageOf(t, m, k.ID); !u.Empty() || !u.LastUsed.IsZero() {
		t.Fatalf("new key must have no usage: %+v", u)
	}
	at := time.Unix(1_700_000_000, 0)
	m.Record(k.ID, Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Requests: 1, LastUsed: at})
	m.Record(k.ID, Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3, Requests: 1, LastUsed: at.Add(-time.Hour)})
	if err := m.FlushUsage(); err != nil {
		t.Fatal(err)
	}
	m.Record(k.ID, Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, Requests: 1})
	m.Record(k.ID, Usage{MCPRequests: 4})
	u := usageOf(t, m, k.ID) // List writes pending increments first
	if u.PromptTokens != 111 || u.CompletionTokens != 57 || u.TotalTokens != 168 || u.Requests != 3 || u.MCPRequests != 4 {
		t.Fatalf("totals: %+v", u)
	}
	if !u.LastUsed.After(at) {
		t.Fatalf("last used must be the newest record: %v", u.LastUsed)
	}
}

func TestUsageConcurrentRecordsAreExact(t *testing.T) {
	m := New(newDB(t))
	_, k, _ := m.Create("a")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				m.Record(k.ID, Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3, Requests: 1})
				if j%7 == 0 {
					_ = m.FlushUsage()
				}
			}
		}()
	}
	wg.Wait()
	u := usageOf(t, m, k.ID)
	if u.Requests != 1000 || u.PromptTokens != 2000 || u.CompletionTokens != 1000 || u.TotalTokens != 3000 {
		t.Fatalf("lost or doubled increments: %+v", u)
	}
}

func TestUsageSurvivesRegenerateAndGoesWithPurgedKey(t *testing.T) {
	db := newDB(t)
	m := New(db)
	_, k, _ := m.Create("a")
	m.Record(k.ID, Usage{TotalTokens: 7, Requests: 1})
	if _, _, err := m.Regenerate(k.ID); err != nil {
		t.Fatal(err)
	}
	if u := usageOf(t, m, k.ID); u.TotalTokens != 7 {
		t.Fatalf("regenerate keeps the record: %+v", u)
	}
	if err := m.Revoke(k.ID); err != nil { // never used in Verify, so the revoke purges it
		t.Fatal(err)
	}
	m.Record(k.ID, Usage{TotalTokens: 1, Requests: 1}) // late increment for a deleted key
	if err := m.FlushUsage(); err != nil {
		t.Fatalf("a deleted key must not break the flush: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM key_usage`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("usage rows must go with the key: n=%d err=%v", n, err)
	}
}
