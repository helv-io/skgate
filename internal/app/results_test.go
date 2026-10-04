package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/oidctest"
)

// rawPost posts without following the redirect that follows a change.
func rawPost(br *browser, path string, v url.Values) *http.Response {
	req, _ := http.NewRequest("POST", br.ts.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, _ := br.do(req)
	return r
}

// Nothing that changes something renders a page: it answers 303 to a screen that can be refreshed. A new key or
// client secret is shown again on every refresh for a while, only to the session that made it.
func TestChangesRedirectToRefreshableScreens(t *testing.T) {
	_, ts, br, csrf := signedIn(t, nil)
	for name, c := range map[string]struct {
		path string
		v    url.Values
		want string
	}{
		"key":    {"/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}}, "sk-"},
		"client": {"/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"c"}, "redirects": {"https://x.example/cb"}, "method": {"client_secret_post"}}, "skc-"},
		"import": {"/admin/upstreams/import", url.Values{"csrf": {csrf}, "json": {`{"mcpServers": {"imp": {"url": "https://h.example.com/mcp"}}}`}}, "imp"},
	} {
		r := rawPost(br, c.path, c.v)
		loc := r.Header.Get("Location")
		if r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/admin/results/") {
			t.Fatalf("%s: %d %q, want 303 to a result screen", name, r.StatusCode, loc)
		}
		if strings.Contains(loc, c.want+"-") && name != "import" {
			t.Errorf("%s: the secret is in the URL %q", name, loc)
		}
		r1, b1 := br.get(loc)
		r2, b2 := br.get(loc) // refresh
		if r1.StatusCode != 200 || r2.StatusCode != 200 || !strings.Contains(b1, c.want) || !strings.Contains(b2, c.want) {
			t.Fatalf("%s: refresh %d/%d lost the result:\n%s", name, r1.StatusCode, r2.StatusCode, b2)
		}
		if (name != "client" && strings.Count(b1, `<div class="toast `) != 1) || strings.Contains(b2, `<div class="toast `) {
			t.Errorf("%s: the notice shows once, not on every refresh", name)
		}
		// another session sees nothing
		other := newBrowser(t, ts)
		if r, _ := other.get(loc); r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), "/admin/oidc/login") {
			t.Errorf("%s: another visitor gets %d %q", name, r.StatusCode, r.Header.Get("Location"))
		}
	}
	// an unknown or foreign token goes back with a message
	if r, _ := br.get("/admin/results/0123456789abcdef"); r.StatusCode != 303 || r.Header.Get("Location") != "/admin" || flashKind(r) != "bad" {
		t.Errorf("unknown result: %d %q %q", r.StatusCode, r.Header.Get("Location"), flashKind(r))
	}
	// creating is never done by GET
	for _, p := range []string{"/admin/keys/create", "/admin/clients/create", "/admin/clients/delete", "/admin/upstreams/save"} {
		if r, _ := br.get(p); r.StatusCode != 405 {
			t.Errorf("GET %s: %d", p, r.StatusCode)
		}
	}
	// regenerating a key shows the new one the same way
	_, keys := br.get("/admin/keys")
	id := between(keys, `name="id" value="`, `"`)
	r := rawPost(br, "/admin/keys/regenerate", url.Values{"csrf": {csrf}, "id": {id}})
	if loc := r.Header.Get("Location"); r.StatusCode != 303 || !strings.HasPrefix(loc, "/admin/results/") {
		t.Fatalf("regenerate: %d %q", r.StatusCode, loc)
	}
}

// Every upstream address carries the alias: screens are GET, changes are POST, and the old query form is gone.
func TestUpstreamRoutesUseTheAlias(t *testing.T) {
	a, _, br, csrf := signedIn(t, func(c *config.Config, _ *oidctest.Provider) {})
	a.Admin.NoSaveTest = true
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"al-1"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"none"}, "enabled": {"1"}})
	for path, method := range map[string]string{"/admin/upstreams/al-1/edit": "GET", "/admin/upstreams/al-1/test": "GET",
		"/admin/upstreams/al-1/toggle": "POST", "/admin/upstreams/al-1/redetect": "POST", "/admin/upstreams/al-1/delete": "POST", "/admin/upstreams/al-1/save": "POST"} {
		other := map[string]string{"GET": "POST", "POST": "GET"}[method]
		req, _ := http.NewRequest(other, br.ts.URL+path, strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if r, _ := br.do(req); r.StatusCode != 405 {
			t.Errorf("%s %s: %d, want 405", other, path, r.StatusCode)
		}
	}
	_, page := br.get("/admin/upstreams")
	if strings.Contains(page, "?alias=") || strings.Contains(page, `name="alias" value="al-1"`) {
		t.Error("the list still carries the alias in a query or a hidden field")
	}
	for _, want := range []string{`action="/admin/upstreams/al-1/toggle"`, `action="/admin/upstreams/al-1/delete"`, `href="/admin/upstreams/al-1/edit"`, `href="/admin/upstreams/al-1/test"`} {
		if !strings.Contains(page, want) {
			t.Errorf("list lacks %s", want)
		}
	}
	for _, old := range []string{"/admin/upstreams/edit?alias=al-1", "/admin/upstreams/logs?alias=al-1", "/admin/upstreams/test", "/admin/upstreams/delete"} {
		if r, _ := br.get(old); r.StatusCode != 404 {
			t.Errorf("%s still answers: %d", old, r.StatusCode)
		}
	}
	// editing posts to the alias; the add route refuses an edit
	if r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"edit"}, "alias": {"al-1"}, "url": {"http://127.0.0.1:1/mcp"}}); r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Errorf("edit through /save: %d %q", r.StatusCode, flashKind(r))
	}
}

// A result belongs to the session that made it: a second signed-in session cannot open it.
func TestResultsBelongToTheirSession(t *testing.T) {
	_, ts, idp := newApp(t, nil)
	one, two := newBrowser(t, ts), newBrowser(t, ts)
	one.sso(idp, "/admin")
	two.sso(idp, "/admin")
	_, home := one.get("/admin")
	csrf := between(home, `name="csrf" value="`, `"`)
	loc := rawPost(one, "/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}}).Header.Get("Location")
	if _, b := one.get(loc); !strings.Contains(b, "sk-") {
		t.Fatal("the owner sees the key")
	}
	if r, b := two.get(loc); strings.Contains(b, "sk-") || r.StatusCode != 303 {
		t.Fatalf("another session: %d", r.StatusCode)
	}
}
