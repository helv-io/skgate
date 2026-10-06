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

	if !strings.Contains(page, "<th>Status</th><th>Name</th><th>Key</th><th>Usage</th><th>Expires</th><th>Last used</th><th class=\"actions-th\">Actions</th>") {
		t.Fatal("Status is the first column and the buttons column is headed Actions")
	}
	i := strings.Index(page, `<tbody id="key-rows"`)
	rows := strings.Split(page[i:], "</tr>")
	row := func(label string) string {
		for _, r := range rows {
			if strings.Contains(r, `data-label="Name">`+label+"</td>") {
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
	if strings.Contains(busy, "<th>last used</th>") || !strings.Contains(page, "<th>Last used</th>") {
		t.Error("the last use is a column of the list (and still ends the Usage tooltip)")
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
	for _, want := range []string{`name="rate" min="0" inputmode="numeric" placeholder="no limit" value="30"`, `name="expires"`,
		`data-expiry-url="/admin/keys/expiry"`, "Max requests per minute", `action="/admin/keys/update"`, `id="key-`, "expires 2031-12-31"} {
		if !strings.Contains(page, want) {
			t.Errorf("keys page lacks %q", want)
		}
	}
	for _, gone := range []string{"<th>rate limit</th>", "<th>expires</th>", "Rate limit (requests per minute", `action="/admin/keys/limits"`, "hard stop", "Hard stop", `name="stop"`, "stop at"} {
		if strings.Contains(page, gone) {
			t.Errorf("keys page still has %q", gone)
		}
	}
	// the dialog of a key with an expiration is prefilled with a text that reads back to the same moment
	if !strings.Contains(page, `name="expires" value="2031-12-31"`) {
		t.Error("the edit form must carry the current expiration")
	}
	br.post("/admin/keys/update", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["free"].ID, 10)}, "label": {"free"}, "rate": {"5"}, "expires": {"30d"}})
	br.post("/admin/keys/update", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["capped"].ID, 10)}, "label": {"capped"}})
	br.post("/admin/keys/update", url.Values{"csrf": {csrf}, "id": {strconv.FormatInt(by["capped"].ID, 10)}, "label": {"renamed"}, "expires": {"yesterday"}})
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

// The edit form of a key is one form with one Save: name, limits and ?key= (a checkbox with a warning, off by
// default), above the read-only Details. The old separate switch and the global route are gone, and a bad value
// saves nothing.
func TestKeyEditFormInKeyDialog(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"one"}})
	ks, _ := a.Keys.List()
	id := strconv.FormatInt(ks[0].ID, 10)
	_, page := br.get("/admin/keys")
	for _, want := range []string{`action="/admin/keys/update"`, `name="label" maxlength="80" value="one"`, `<input type="checkbox" name="urlkey" value="1" >`, "browser history"} {
		if !strings.Contains(page, want) {
			t.Errorf("keys page lacks %q", want)
		}
	}
	for _, gone := range []string{"/admin/settings/query-key", "/admin/keys/urlkey", "?key= in the URL: off"} {
		if strings.Contains(page, gone) {
			t.Errorf("keys page still has %q", gone)
		}
	}
	if strings.Count(page, `action="/admin/keys/update"`) != 1 {
		t.Error("one edit form per key")
	}
	i := strings.Index(page, `action="/admin/keys/update"`)
	if i > strings.Index(page, "<h4>Details</h4>") || strings.Index(page[i:], "action=\"/admin/keys/revoke\"") > strings.Index(page[i:], "<h4>Details</h4>") {
		t.Error("the editable part, with revoke and regenerate, belongs above Details")
	}
	br.post("/admin/keys/update", url.Values{"csrf": {csrf}, "id": {id}, "label": {"Home Assistant"}, "rate": {"7"}, "urlkey": {"1"}})
	ks, _ = a.Keys.List()
	if k := ks[0]; k.Label != "Home Assistant" || k.RatePerMin != 7 || !k.URLKey {
		t.Fatalf("Save did not store all three: %+v", k)
	}
	if _, page = br.get("/admin/keys"); !strings.Contains(page, `<input type="checkbox" name="urlkey" value="1" checked>`) {
		t.Error("the dialog must show the state")
	}
	br.post("/admin/keys/update", url.Values{"csrf": {csrf}, "id": {id}, "label": {"Home Assistant"}})
	if ks, _ = a.Keys.List(); ks[0].URLKey || ks[0].Limited() {
		t.Fatalf("an unchecked box turns ?key= off and an empty rate clears the limit: %+v", ks[0])
	}
	br.post("/admin/keys/update", url.Values{"csrf": {csrf}, "id": {id}, "label": {"other"}, "rate": {"x"}, "urlkey": {"1"}})
	if ks, _ = a.Keys.List(); ks[0].Label != "Home Assistant" || ks[0].URLKey {
		t.Fatalf("a bad value must save nothing: %+v", ks[0])
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

// A key that may be sent as ?key= gets the geturl marker on its row (and so on its card on phones); the tint is one
// shared token used by one rule for the table row and one for the mobile card.
func TestURLKeyRowIsMarked(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"plain"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"geturl"}})
	ks, _ := a.Keys.List()
	for _, k := range ks {
		if k.Label == "geturl" {
			a.Keys.SetURLKey(k.ID, true)
		}
	}
	_, page := br.get("/admin/keys")
	if n := strings.Count(page, `<tr class="geturl"`); n != 1 {
		t.Fatalf("exactly the enabled key's row carries the marker, got %d", n)
	}
	css, _ := os.ReadFile("../admin/static/app.css")
	c := string(css)
	if !strings.Contains(c, "--tint-url:") || strings.Count(c, "var(--tint-url)") < 3 {
		t.Error("the tint must be a shared token used by the table and the card rules")
	}
	for _, sel := range []string{".table tbody tr.geturl", ".table:not(.kv) tbody tr.geturl"} {
		if !strings.Contains(c, sel) {
			t.Errorf("no rule for %q", sel)
		}
	}
}

// The list shows when each key ends and when it was last used; a key that ends within a week (or already has)
// is colored, because that is the one that breaks a client unannounced.
func TestKeysListShowsExpiryAndLastUse(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	for _, l := range []string{"soon", "later", "forever", "past", "used"} {
		br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {l}})
	}
	ks, _ := a.Keys.List()
	id := map[string]vkeys.Key{}
	for _, k := range ks {
		id[k.Label] = k
	}
	a.Keys.SetLimits(id["soon"].ID, 0, time.Now().Add(3*24*time.Hour))
	a.Keys.SetLimits(id["later"].ID, 0, time.Now().Add(30*24*time.Hour))
	a.Keys.SetLimits(id["past"].ID, 0, time.Now().Add(-time.Hour))
	full, used, _ := a.Keys.Create("used")
	a.Keys.Verify(full)
	_ = used
	_, page := br.get("/admin/keys")
	cell := func(label, col string) string {
		for _, r := range strings.Split(page[strings.Index(page, `<tbody id="key-rows"`):], "</tr>") {
			if strings.Contains(r, `data-label="Name">`+label+"</td>") {
				i := strings.Index(r, `data-label="`+col+`"`)
				if i < 0 {
					t.Fatalf("no %s cell in %s", col, r)
				}
				j := strings.LastIndex(r[:i], "<td")
				return r[j : i+strings.Index(r[i:], "</td>")+5]
			}
		}
		t.Fatalf("no row %s", label)
		return ""
	}
	if c := cell("soon", "Expires"); !strings.Contains(c, `nw warn`) || !strings.Contains(c, `title="expires in 3 days"`) {
		t.Errorf("a key ending within a week is called out: %s", c)
	}
	if c := cell("later", "Expires"); strings.Contains(c, "warn") || strings.Contains(c, "muted") || strings.Contains(c, "bad") {
		t.Errorf("a key ending later is plain: %s", c)
	}
	if c := cell("forever", "Expires"); !strings.Contains(c, "muted") || !strings.Contains(c, ">never<") {
		t.Errorf("never: %s", c)
	}
	if c := cell("past", "Expires"); !strings.Contains(c, "nw bad") || !strings.Contains(c, `title="expired"`) {
		t.Errorf("an expired key: %s", c)
	}
	if c := cell("forever", "Last used"); !strings.Contains(c, "muted") || !strings.Contains(c, ">never<") {
		t.Errorf("never used: %s", c)
	}
	if c := cell("used", "Last used"); strings.Contains(c, "never") || !strings.Contains(c, time.Now().Format("2006-01-02")) {
		t.Errorf("last used shows the date: %s", c)
	}
}
