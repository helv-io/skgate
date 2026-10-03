package app

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/vkeys"
)

func TestKeysPageUsageColumn(t *testing.T) {
	a, _, br, _ := signedIn(t, nil)
	used, usedKey, _ := a.Keys.Create("busy")
	_, mcpOnly, _ := a.Keys.Create("mcp")
	_, idle, _ := a.Keys.Create("idle")
	_ = idle
	a.Keys.Record(usedKey.ID, vkeys.Usage{PromptTokens: 1_234_567, CompletionTokens: 340_000, TotalTokens: 1_574_567, Requests: 5,
		LastUsed: time.Date(2026, 10, 2, 11, 0, 0, 0, time.Local)})
	a.Keys.Record(mcpOnly.ID, vkeys.Usage{MCPRequests: 3})
	_, page := br.get("/admin/keys")

	if !strings.Contains(page, "<th>Last used</th><th>Usage</th><th>Status</th>") {
		t.Fatal("Usage must sit directly before Status")
	}
	i := strings.Index(page, "<tbody>")
	rows := strings.Split(page[i:], "</tr>")
	row := func(label string) string {
		for _, r := range rows {
			if strings.Contains(r, "<td>"+label+"</td>") {
				return r
			}
		}
		t.Fatalf("no row for %s", label)
		return ""
	}
	busy := row("busy")
	if !strings.Contains(busy, `>1.2M in / 340K out</span>`) {
		t.Errorf("compact input/output cell missing:\n%s", busy)
	}
	for _, want := range []string{"Input tokens: 1,234,567", "Output tokens: 340,000", "Total tokens: 1,574,567", "API requests: 5"} {
		if !strings.Contains(busy, want) {
			t.Errorf("tooltip lacks %q", want)
		}
	}
	if strings.Contains(busy, "Last used:") || strings.Contains(busy, "MCP requests") || strings.Contains(busy, "style=") {
		t.Error("no last-used line (own column), no MCP line without MCP requests, no inline styles")
	}
	if !strings.Contains(busy, "************"+used[len(used)-4:]) || strings.Contains(page, used) {
		t.Error("keys stay masked")
	}
	m := row("mcp")
	if !strings.Contains(m, `class="tip" title="`) || !strings.Contains(m, "MCP requests: 3") || !strings.Contains(m, ">\u2014</span>") {
		t.Errorf("MCP-only key shows an em-dash and keeps its tooltip:\n%s", m)
	}
	if id := row("idle"); !strings.Contains(id, "<span class=\"tip\">\u2014</span>") {
		t.Errorf("unused key shows a bare em-dash:\n%s", id)
	}
}

// Limits are optional: the create form and the per-key dialog set them, an empty value means unlimited, bad
// values are refused, and the table shows them.
func TestKeyLimitsInAdmin(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"free"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"capped"}, "rate": {"30"}, "stop": {"1000"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"bad"}, "rate": {"-4"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"bad2"}, "stop": {"lots"}})
	ks, _ := a.Keys.List()
	if len(ks) != 2 {
		t.Fatalf("invalid limits must not create a key: %d keys", len(ks))
	}
	by := map[string]vkeys.Key{}
	for _, k := range ks {
		by[k.Label] = k
	}
	if by["free"].Limited() || by["capped"].RatePerMin != 30 || by["capped"].HardStop != 1000 {
		t.Fatalf("%+v", by)
	}
	_, page := br.get("/admin/keys")
	for _, want := range []string{"<th>Limits</th>", "30/min, stop at 1000", ">unlimited<", `name="rate"`, `name="stop"`, `action="/admin/keys/limits"`, `id="key-limits-`} {
		if !strings.Contains(page, want) {
			t.Errorf("keys page lacks %q", want)
		}
	}
	br.post("/admin/keys/limits", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["free"].ID, 10)}, "rate": {"5"}})
	br.post("/admin/keys/limits", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["capped"].ID, 10)}})
	ks, _ = a.Keys.List()
	for _, k := range ks {
		switch k.Label {
		case "free":
			if k.RatePerMin != 5 || k.HardStop != 0 {
				t.Errorf("free: %+v", k)
			}
		case "capped":
			if k.Limited() {
				t.Errorf("capped must be cleared: %+v", k)
			}
		}
	}
}
