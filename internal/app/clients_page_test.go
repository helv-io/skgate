package app

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/mcp"
)

func seedClient(t *testing.T, a *App, id, source string, created, used time.Time, uris ...string) {
	t.Helper()
	if _, err := a.MCP.Clients.Create(mcp.Client{ID: id, Name: "name-" + id, RedirectURIs: uris, AuthMethod: "none", Source: source}, ""); err != nil {
		t.Fatal(err)
	}
	a.DB.Exec(`UPDATE oauth_clients SET created_at=? WHERE client_id=?`, created.Unix(), id)
	if !used.IsZero() {
		a.DB.Exec(`UPDATE oauth_clients SET last_used_at=? WHERE client_id=?`, used.Unix(), id)
	}
}

// The OAuth clients list: most recently used first (never used last, newest first), plain chips for where a client
// came from, created and last used columns and the host of the redirect.
func TestClientsListColumnsOrderAndChips(t *testing.T) {
	a, _, br, _ := signedIn(t, nil)
	now := time.Now()
	seedClient(t, a, "old-used", "dcr", now.Add(-90*24*time.Hour), now.Add(-3*time.Hour), "https://claude.ai/api/mcp/auth_callback")
	seedClient(t, a, "fresh-used", "admin", now.Add(-60*24*time.Hour), now.Add(-5*time.Minute), "https://my.home-assistant.io/redirect/oauth", "http://localhost:8123/cb")
	seedClient(t, a, "never-a", "dcr", now.Add(-10*24*time.Hour), time.Time{}, "https://a.example/cb")
	seedClient(t, a, "never-b", "cimd", now.Add(-1*24*time.Hour), time.Time{}, "https://b.example/cb")
	_, page := br.get("/admin/clients")
	last, order := -1, []string{"name-fresh-used", "name-old-used", "name-never-b", "name-never-a"}
	for _, n := range order {
		i := strings.Index(page, `data-label="Name">`+n+"</td>")
		if i < 0 || i < last {
			t.Fatalf("%s is missing or out of order (want %v):\n%s", n, order, page)
		}
		last = i
	}
	for _, w := range []string{`>self-registered</span>`, `>created here</span>`, `>metadata document</span>`,
		`<th>Source</th><th>Redirect host</th><th>Created</th><th>Last used</th>`,
		`<code title="my.home-assistant.io">my.home-assistant.io</code> <span class="muted">+1 more</span>`,
		`<code title="claude.ai">claude.ai</code></td>`, `data-label="Last used" class="nw muted">never`} {
		if !strings.Contains(page, w) && !strings.Contains(page, strings.Replace(w, `data-label="Last used" class="nw muted"`, `class="nw muted" data-label="Last used"`, 1)) {
			t.Errorf("clients page misses %q", w)
		}
	}
	if strings.Contains(page, `<span class="chip">dcr</span>`) || strings.Contains(page, ">admin</span>") {
		t.Error("the raw source names must not show")
	}
	if !strings.Contains(page, "title=\"the client registered itself") {
		t.Error("a chip explains itself on hover")
	}
}

// Delete unused: clients last used (or, never used, created) more than 30 days ago go with their tokens, after a
// confirmation that says how many; the button is disabled when none qualify; it is a POST with the CSRF token.
func TestClientsDeleteUnusedForThirtyDays(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	_, page := br.get("/admin/clients")
	if !strings.Contains(page, `action="/admin/clients/delete-unused"`) || !strings.Contains(page, "disabled title=\"no client has gone unused for 30 days\"") {
		t.Fatalf("the bulk delete is there and disabled with nothing to delete:\n%s", page)
	}
	now := time.Now()
	seedClient(t, a, "stale-never", "dcr", now.Add(-40*24*time.Hour), time.Time{}, "https://a.example/cb")
	seedClient(t, a, "stale-used", "admin", now.Add(-80*24*time.Hour), now.Add(-35*24*time.Hour), "https://b.example/cb")
	seedClient(t, a, "recent", "dcr", now.Add(-80*24*time.Hour), now.Add(-2*24*time.Hour), "https://c.example/cb")
	seedClient(t, a, "new", "dcr", now.Add(-2*time.Hour), time.Time{}, "https://d.example/cb")
	_, page = br.get("/admin/clients")
	if !strings.Contains(page, `data-confirm="Delete 2 clients unused for 30 days, with their tokens?"`) || !strings.Contains(page, ">delete unused for 30 days (2)</button>") {
		t.Fatalf("the confirmation says how many:\n%s", page)
	}
	if r, _ := br.get("/admin/clients/delete-unused"); r.StatusCode != 405 {
		t.Errorf("GET: %d", r.StatusCode)
	}
	if r, _ := br.post("/admin/clients/delete-unused", url.Values{}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	r, _ := br.post("/admin/clients/delete-unused", url.Values{"csrf": {csrf}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/clients" {
		t.Fatalf("delete unused: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if k, m := flashOf(r); k != "ok" || m != "2 clients deleted" {
		t.Errorf("toast %q %q", k, m)
	}
	for id, want := range map[string]bool{"stale-never": false, "stale-used": false, "recent": true, "new": true} {
		if _, ok := a.MCP.Clients.Get(id); ok != want {
			t.Errorf("%s present = %v, want %v", id, ok, want)
		}
	}
	r, _ = br.post("/admin/clients/delete-unused", url.Values{"csrf": {csrf}})
	if _, m := flashOf(r); m != "no unused clients" {
		t.Errorf("second run toast %q", m)
	}
}
