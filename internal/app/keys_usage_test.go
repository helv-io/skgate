package app

import (
	"encoding/json"
	"net/url"
	"os"
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

	if !strings.Contains(page, "<th>Status</th><th>Key</th><th>Label</th><th>Usage</th><th class=\"actions-th\">Actions</th>") {
		t.Fatal("Status is the first column and the buttons column is headed Actions")
	}
	i := strings.Index(page, "<tbody>")
	rows := strings.Split(page[i:], "</tr>")
	row := func(label string) string {
		for _, r := range rows {
			if strings.Contains(r, `data-label="Label">`+label+"</td>") {
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
	if !strings.Contains(busy, "Last used: 2026-10-02") || strings.Contains(busy, "MCP requests") || strings.Contains(busy, "style=") {
		t.Error("the tooltip ends with the last use, has no MCP line without MCP requests, and no inline styles")
	}
	if strings.Contains(busy, "<th>last used</th>") || strings.Contains(page, "<th>Last used</th>") {
		t.Error("the last use lives in the Usage tooltip, not in a column")
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

// The rate limit and the expiration are optional: the create form and the per-key dialog set them, an empty
// value means none, bad values are refused (and create nothing), and the table and Details show them.
func TestKeyLimitsInAdmin(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"free"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"capped"}, "rate": {"30"}, "expires": {"2031-12-31"}})
	for name, v := range map[string]url.Values{"bad": {"rate": {"-4"}}, "bad2": {"expires": {"someday"}}, "bad3": {"expires": {"2020-01-01"}}, "bad4": {"rate": {"x"}}} {
		v.Set("csrf", csrf)
		v.Set("label", name)
		br.post("/admin/keys/create", v)
	}
	ks, _ := a.Keys.List()
	if len(ks) != 2 {
		t.Fatalf("invalid limits must not create a key: %d keys", len(ks))
	}
	by := map[string]vkeys.Key{}
	for _, k := range ks {
		by[k.Label] = k
	}
	end := time.Date(2031, 12, 31, 23, 59, 59, 0, time.Local)
	if by["free"].Limited() || !by["free"].ExpiresAt.IsZero() || by["capped"].RatePerMin != 30 || !by["capped"].ExpiresAt.Equal(end) {
		t.Fatalf("%+v", by)
	}
	_, page := br.get("/admin/keys")
	for _, want := range []string{"<th>rate limit</th>", "<th>expires</th>", "30/min", ">unlimited<", ">never<", "2031-12-31 23:59", `name="rate"`, `name="expires"`,
		`data-expiry-url="/admin/keys/expiry"`, "Rate limit (requests per minute", `action="/admin/keys/limits"`, `id="key-`, "expires 2031-12-31"} {
		if !strings.Contains(page, want) {
			t.Errorf("keys page lacks %q", want)
		}
	}
	for _, gone := range []string{"hard stop", "Hard stop", `name="stop"`, "stop at"} {
		if strings.Contains(page, gone) {
			t.Errorf("keys page still has %q", gone)
		}
	}
	// the dialog of a key with an expiration is prefilled with a text that reads back to the same moment
	if !strings.Contains(page, `name="expires" value="2031-12-31"`) {
		t.Error("the Limits form must carry the current expiration")
	}
	br.post("/admin/keys/limits", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["free"].ID, 10)}, "rate": {"5"}, "expires": {"30d"}})
	br.post("/admin/keys/limits", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["capped"].ID, 10)}})
	br.post("/admin/keys/limits", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["capped"].ID, 10)}, "expires": {"yesterday"}})
	ks, _ = a.Keys.List()
	for _, k := range ks {
		switch k.Label {
		case "free":
			if k.RatePerMin != 5 || time.Until(k.ExpiresAt) < 29*24*time.Hour || time.Until(k.ExpiresAt) > 31*24*time.Hour {
				t.Errorf("free: %+v", k)
			}
		case "capped":
			if k.Limited() || !k.ExpiresAt.IsZero() {
				t.Errorf("capped must be cleared (and a bad value changes nothing): %+v", k)
			}
		}
	}
}

// An expired key shows "expired" in the table and Details, with the date; the date also shows in Details for a live one.
func TestExpiredKeyShowsInTheKeysTable(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"old"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"fresh"}, "expires": {"7d"}})
	ks, _ := a.Keys.List()
	for _, k := range ks {
		if k.Label == "old" {
			a.Keys.SetLimits(k.ID, 0, time.Now().Add(-time.Hour))
		}
	}
	_, page := br.get("/admin/keys")
	if !strings.Contains(page, `<span class="pill warn" title="expired `) || !strings.Contains(page, ">expired</span>") {
		t.Errorf("an expired key must say so:\n%s", page)
	}
	if strings.Count(page, ">active</span>") < 2 { // fresh: table and Details
		t.Error("a key that has not expired is active")
	}
}

// The preview endpoint reads the text with the same parser the forms use.
func TestExpiryPreviewEndpoint(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	get := func(q string) (ok, never bool, text string) {
		_, body := br.get("/admin/keys/expiry?q=" + url.QueryEscape(q))
		var j struct {
			OK    bool   `json:"ok"`
			Never bool   `json:"never"`
			Text  string `json:"text"`
		}
		if err := json.Unmarshal([]byte(body), &j); err != nil {
			t.Fatalf("%q: %v\n%s", q, err, body)
		}
		return j.OK, j.Never, j.Text
	}
	if ok, _, text := get("2031-12-31"); !ok || !strings.HasPrefix(text, "Expires Wed Dec 31, 2031, 11:59 PM ") {
		t.Errorf("date: %v %q", ok, text)
	}
	if ok, _, text := get("30d"); !ok || !strings.HasPrefix(text, "Expires ") {
		t.Errorf("relative: %v %q", ok, text)
	}
	if ok, never, text := get(""); !ok || !never || text != "Never expires" {
		t.Errorf("empty: %v %v %q", ok, never, text)
	}
	for _, bad := range []string{"soonish", "12/31/2026", "2020-01-01"} {
		if ok, _, text := get(bad); ok || text == "" {
			t.Errorf("%q must be refused with a hint: %q", bad, text)
		}
	}
	if ok, _, text := get("soonish"); ok || !strings.Contains(text, "30d") {
		t.Errorf("the hint names examples: %q", text)
	}
	// admin only, GET only
	anon := newBrowser(t, ts)
	if resp, _ := anon.get("/admin/keys/expiry?q=1d"); resp.StatusCode == 200 {
		t.Error("the preview must need an admin session")
	}
}

// ?key= is a per-key switch in the key's Details dialog (editable part, with a warning), off by default; the old
// global route is gone.
func TestURLKeySwitchInKeyDialog(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"one"}})
	ks, _ := a.Keys.List()
	id := strconv.FormatInt(ks[0].ID, 10)
	_, page := br.get("/admin/keys")
	for _, want := range []string{`action="/admin/keys/urlkey"`, "browser history and referrers", "?key= in the URL: off"} {
		if !strings.Contains(page, want) {
			t.Errorf("keys page lacks %q", want)
		}
	}
	if strings.Contains(page, "/admin/settings/query-key") {
		t.Error("the global switch must be gone")
	}
	if strings.Index(page, `action="/admin/keys/urlkey"`) > strings.Index(page, "<h4>Details</h4>") {
		t.Error("the switch belongs to the editable part, above Details")
	}
	br.post("/admin/keys/urlkey", url.Values{"csrf": {csrf}, "id": {id}, "allow": {"1"}})
	if ks, _ = a.Keys.List(); !ks[0].URLKey {
		t.Fatal("the switch did not turn on")
	}
	if _, page = br.get("/admin/keys"); !strings.Contains(page, "?key= in the URL: allowed") {
		t.Error("the dialog must show the state")
	}
	br.post("/admin/keys/urlkey", url.Values{"csrf": {csrf}, "id": {id}, "allow": {"0"}})
	if ks, _ = a.Keys.List(); ks[0].URLKey {
		t.Fatal("the switch did not turn off")
	}
	if resp, _ := br.post("/admin/settings/query-key", url.Values{"csrf": {csrf}}); resp.StatusCode == 200 || resp.StatusCode == 303 {
		t.Errorf("the global route must be gone: %d", resp.StatusCode)
	}
}

// The header version link only gets a pointer cursor on hover: no box, underline or color change.
func TestVersionLinkHasNoHoverStyling(t *testing.T) {
	css, err := os.ReadFile("../admin/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	i := strings.Index(s, "header a.ver,header a.ver:hover{")
	if i < 0 {
		t.Fatal("version link hover rule missing")
	}
	rule := s[i : i+strings.Index(s[i:], "}")]
	for _, want := range []string{"background:none", "color:var(--dim)", "text-decoration:none", "cursor:pointer"} {
		if !strings.Contains(rule, want) {
			t.Errorf("version link hover rule lacks %q", want)
		}
	}
	if strings.Contains(s, "a.ver:hover{background:none;color:var(--acc)") {
		t.Error("the version link must not change color on hover")
	}
}
