package app

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var tokenRe = regexp.MustCompile(`class="copybox" data-copy-text="(sk-[A-Za-z0-9_-]+)"`)

func TestKeyCreateShowsTokenOnceWithCopyButton(t *testing.T) {
	_, ts, br, csrf := signedIn(t, nil)
	r, page := br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"svc"}})
	m := tokenRe.FindStringSubmatch(page)
	if r.StatusCode != 200 || m == nil {
		t.Fatalf("token not shown: %d", r.StatusCode)
	}
	if !strings.Contains(page, `aria-label="Copy the new key"`) || !strings.Contains(page, "Tap to copy") {
		t.Error("one-time token needs a Copy button")
	}
	if _, again := br.get("/admin/keys"); strings.Contains(again, m[1]) {
		t.Error("token must be shown only once")
	}
	r2, _ := http.Get(ts.URL + "/admin/static/app.js")
	js, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	for _, want := range []string{"navigator.clipboard", "execCommand", "data-copy"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestKeyRegenerateFlow(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	_, page := br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"svc"}})
	old := tokenRe.FindStringSubmatch(page)[1]
	ks, _ := a.Keys.List()
	id := ks[0].ID
	idS := url.Values{"id": {itoa(id)}}
	// POST + CSRF only
	if r, _ := br.get("/admin/keys/regenerate?id=" + itoa(id)); r.StatusCode != 405 {
		t.Errorf("GET: %d", r.StatusCode)
	}
	if r, _ := br.post("/admin/keys/regenerate", idS); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	idS.Set("csrf", csrf)
	r, page := br.post("/admin/keys/regenerate", idS)
	m := tokenRe.FindStringSubmatch(page)
	if r.StatusCode != 200 || m == nil || m[1] == old {
		t.Fatalf("regenerate: %d, new token %v", r.StatusCode, m)
	}
	if !strings.Contains(page, `aria-label="Copy the new key"`) {
		t.Error("regenerated token needs a Copy button")
	}
	if _, ok := a.Keys.Verify(old); ok {
		t.Error("old token still works")
	}
	if k, ok := a.Keys.Verify(m[1]); !ok || k.ID != id || k.Label != "svc" {
		t.Error("new token must verify as the same record")
	}
	if l, _ := a.Keys.List(); len(l) != 1 {
		t.Errorf("regenerate must not add records: %d", len(l))
	}
	if !strings.Contains(page, "/admin/keys/regenerate") || !strings.Contains(page, "/admin/keys/revoke") {
		t.Error("keys list needs regenerate and revoke actions")
	}
	if _, again := br.get("/admin/keys"); strings.Contains(again, m[1]) {
		t.Error("token shown again")
	}
	// unknown key: error toast via redirect
	r, _ = br.post("/admin/keys/regenerate", url.Values{"csrf": {csrf}, "id": {"999"}})
	if r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Errorf("unknown id: %d %q", r.StatusCode, flashKind(r))
	}
}

func TestKeyRevokeRemovesNeverUsedKey(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"x"}})
	ks, _ := a.Keys.List()
	r, _ := br.post("/admin/keys/revoke", url.Values{"csrf": {csrf}, "id": {itoa(ks[0].ID)}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/keys" || flashKind(r) != "ok" {
		t.Fatalf("revoke: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if l, _ := a.Keys.List(); len(l) != 0 {
		t.Fatalf("never-used revoked key must be deleted: %+v", l)
	}
}

func TestClientsPageShowsLastUsed(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"fresh"}, "redirects": {"https://c.example/cb"}, "method": {"none"}})
	_, page := br.get("/admin/clients")
	if !strings.Contains(page, "<th>last used</th>") || !regexp.MustCompile(`<th>last used</th><td>-</td>`).MatchString(page) {
		t.Fatalf("never-used client must show a dash:\n%s", page)
	}
	l, _ := a.MCP.Clients.List()
	a.MCP.Clients.Touch(l[0].ID)
	_, page = br.get("/admin/clients")
	if strings.Contains(page, "<th>last used</th><td>-</td>") {
		t.Fatal("used client still shows a dash")
	}
	if !strings.Contains(page, time.Now().Format("2006-01-02")) {
		t.Fatal("last used time not rendered")
	}
}

// A row has one button (edit; details for a revoked key). Regenerate and revoke are in the key's dialog, behind a
// confirmation, so a destructive button is never one stray click away; a revoked key has nothing left to edit.
func TestKeyRowHasOneAction(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"live"}})
	full, gone, _ := a.Keys.Create("gone")
	a.Keys.Verify(full) // a used key stays listed after it is revoked
	a.Keys.Revoke(gone.ID)
	_, page := br.get("/admin/keys")
	table := page[strings.Index(page, `<tbody id="key-rows"`):strings.Index(page, "</tbody>")]
	if strings.Contains(table, "<form") || strings.Count(table, "data-dialog-open") != 2 {
		t.Errorf("the table rows hold one dialog button each and no forms:\n%s", table)
	}
	if !strings.Contains(table, ">Edit</button>") || !strings.Contains(table, ">Details</button>") {
		t.Error("an active key says edit, a revoked one details")
	}
	if strings.Count(page, `action="/admin/keys/update"`) != 1 || strings.Count(page, `action="/admin/keys/revoke"`) != 1 {
		t.Error("only the active key's dialog has the edit form, regenerate and revoke")
	}
}

// The create form asks for a name and nothing else up front; the limits are optional and folded away (a key is
// unlimited and never expires by default), and a key can be created from the name alone, or from nothing.
func TestKeyCreateFormIsJustAName(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	_, page := br.get("/admin/keys")
	form := page[strings.Index(page, `action="/admin/keys/create"`):]
	form = form[:strings.Index(form, "</form>")]
	open := strings.Index(form, "<details>")
	if open < 0 || strings.Index(form, `name="label"`) > open || strings.Index(form, `name="rate"`) < open || strings.Index(form, `name="expires"`) < open {
		t.Errorf("name first, limits inside a closed details:\n%s", form)
	}
	if strings.Contains(form, "<details open") {
		t.Error("the limits start folded")
	}
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"My assistant"}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}})
	ks, _ := a.Keys.List()
	if len(ks) != 2 || ks[1].Label != "My assistant" || ks[0].Label != "unnamed" || ks[1].Limited() || !ks[1].ExpiresAt.IsZero() {
		t.Fatalf("defaults: %+v", ks)
	}
}
