package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Every admin page answers the same with and without a trailing slash.
func TestAdminTrailingSlash(t *testing.T) {
	_, ts, idp := newApp(t, nil)
	anon := newBrowser(t, ts)
	for _, p := range []string{"/admin", "/admin/keys", "/admin/upstreams", "/admin/clients", "/admin/upstreams/x/edit", "/admin/providers/grok/device/state"} {
		r1, _ := anon.get(p)
		r2, _ := anon.get(p + "/")
		if r1.StatusCode != 302 || r2.StatusCode != r1.StatusCode || r2.Header.Get("Location") != r1.Header.Get("Location") {
			t.Errorf("%s: %d %q vs with slash %d %q", p, r1.StatusCode, r1.Header.Get("Location"), r2.StatusCode, r2.Header.Get("Location"))
		}
	}
	if r, _ := anon.get("/admin/keys/?x=1"); r.Header.Get("Location") != "/admin/oidc/login?next=%2Fadmin%2Fkeys%3Fx%3D1" {
		t.Errorf("query kept, slash dropped from the return path: %q", r.Header.Get("Location"))
	}

	br := newBrowser(t, ts)
	br.sso(idp, "/admin")
	_, home := br.get("/admin")
	csrf := between(home, `name="csrf" value="`, `"`)
	for _, p := range []string{"/admin", "/admin/keys", "/admin/upstreams", "/admin/clients", "/admin/upstreams/x/edit"} {
		path, q, _ := strings.Cut(p, "?")
		if q != "" {
			q = "?" + q
		}
		r1, b1 := br.get(path + q)
		r2, b2 := br.get(path + "/" + q)
		r3, b3 := br.get(path + "//" + q)
		if r1.StatusCode != r2.StatusCode || r1.StatusCode != r3.StatusCode || r1.StatusCode == 404 {
			t.Errorf("%s: %d / %d / %d", p, r1.StatusCode, r2.StatusCode, r3.StatusCode)
		}
		if t1, t2 := between(b1, "<title>", "</title>"), between(b2, "<title>", "</title>"); t1 != t2 || t1 != between(b3, "<title>", "</title>") {
			t.Errorf("%s: title %q vs %q", p, t1, t2)
		}
	}
	// a POST to a slashed path is handled, not redirected
	r, _ := br.post("/admin/keys/create/", url.Values{"label": {"slash"}, "csrf": {csrf}})
	if r.StatusCode != 200 {
		t.Errorf("POST /admin/keys/create/: %d", r.StatusCode)
	}
	// the static directory form is left to the file server
	r, _ = br.get("/admin/static/app.css")
	if r.StatusCode != 200 {
		t.Errorf("static asset: %d", r.StatusCode)
	}
}

// Nothing outside /admin changes: MCP, OAuth, discovery and the API keep their own slash handling.
func TestSlashOutsideAdminUnchanged(t *testing.T) {
	_, ts, _ := newApp(t, nil)
	b := newBrowser(t, ts)
	for _, c := range []struct {
		path string
		want int
	}{
		{"/mcp", 401}, {"/mcp/", 401}, {"/mcp/notes", 401}, {"/mcp/notes/", 401},
		{"/v1/models", 401}, {"/v1/models/", 401}, {"/v1/chat/completions", 401},
		{"/.well-known/oauth-protected-resource", 404},
		{"/.well-known/oauth-protected-resource/mcp", 200},
		{"/.well-known/oauth-authorization-server", 200},
		{"/healthz", 200}, {"/favicon.svg", 200}, {"/admin/static/app.css", 200},
	} {
		if r, _ := b.get(c.path); r.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", c.path, r.StatusCode, c.want)
		}
	}
	if r, _ := b.get("/"); r.StatusCode != http.StatusFound || r.Header.Get("Location") != "/admin" {
		t.Errorf("root: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
}
