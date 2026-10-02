package app

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/oidctest"
)

// The admin app is styled by one shared stylesheet: every page uses the same components, and no
// page carries one-off inline styles, legacy classes or bare tables and buttons.
func TestAdminSharedDesignAcrossPages(t *testing.T) {
	a, ts, br, csrf := signedIn(t, func(c *config.Config, _ *oidctest.Provider) {
		c.ManagedDir = t.TempDir()
	})
	t.Cleanup(a.MCP.ShutdownManaged)
	up := fakeUpstream(t)
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "one", URL: up.URL, AuthKind: mcp.AuthBearer, AuthValue: "sekret-value-1234", Enabled: true, IncludeInMCP: true})
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "two", URL: up.URL + "/a/very/long/path/that/would/otherwise/break/the/table/layout/of/the/page", AuthKind: mcp.AuthNone})
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "mgd", Kind: mcp.KindStdio, Command: os.Args[0], Env: []mcp.KV{{Name: "FAKE_MCP", Value: "1"}, {Name: "API_TOKEN", Value: "env-secret-value-9876"}}, Enabled: true})
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "repo", Kind: mcp.KindGit, Command: "python3", Args: []string{"-m", "srv"}, GitURL: "https://git.example.com/org/repo.git", GitToken: "git-secret-token-5555", Enabled: true})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	pages := map[string]string{}
	for _, p := range []string{"/admin", "/admin/keys", "/admin/upstreams", "/admin/upstreams/edit?alias=one", "/admin/upstreams/edit?alias=mgd", "/admin/upstreams/edit?alias=repo",
		"/admin/upstreams/logs?alias=mgd", "/admin/upstreams/import", "/admin/clients"} {
		_, pages[p] = br.get(p)
	}
	_, pages["/admin/upstreams/test"] = br.post("/admin/upstreams/test", url.Values{"csrf": {csrf}, "alias": {"one"}})
	_, pages["/admin/upstreams/test (managed)"] = br.post("/admin/upstreams/test", url.Values{"csrf": {csrf}, "alias": {"mgd"}})
	_, pages["/admin/upstreams/import (result)"] = br.post("/admin/upstreams/import", url.Values{"csrf": {csrf}, "json": {`{"x": {"command": "y"}, "one": {"command": "z"}, "q": {}}`}})
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, _ := anon.Get(ts.URL + "/admin/signed-out")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	pages["/admin/signed-out"] = string(b)

	legacy := []*regexp.Regexp{
		regexp.MustCompile(`style="`), regexp.MustCompile(`<section`), regexp.MustCompile(`class="link`), regexp.MustCompile(`class="dim"`),
		regexp.MustCompile(`class="flash"`), regexp.MustCompile(`class="err"`), regexp.MustCompile(`<table>`), regexp.MustCompile(`<button>`),
		regexp.MustCompile(`<button class="danger"`), regexp.MustCompile(`class="alert`), regexp.MustCompile(`\?(ok|err)=`),
	}
	for name, page := range pages {
		for _, re := range legacy {
			if re.MatchString(page) {
				t.Errorf("%s uses a legacy or page-specific construct %q", name, re)
			}
		}
	}
	for _, name := range []string{"/admin/keys", "/admin/upstreams", "/admin/clients", "/admin/upstreams/test", "/admin/upstreams/test (managed)", "/admin/upstreams/logs?alias=mgd", "/admin/upstreams/import (result)"} {
		if !strings.Contains(pages[name], `class="table`) || !strings.Contains(pages[name], `tablewrap`) || !strings.Contains(pages[name], "<thead>") {
			t.Errorf("%s: tables must use .table inside .tablewrap with a thead", name)
		}
	}
	ups := pages["/admin/upstreams"]
	for _, want := range []string{`class="act toggle on"`, `class="act toggle off"`, `class="chip"`, `class="actions"`, `class="act"`, `class="act danger"`} {
		if !strings.Contains(ups, want) {
			t.Errorf("upstreams page lacks %q", want)
		}
	}
	// low-priority upstream columns are hidden on narrow windows; the URL column shrinks with an ellipsis
	for _, want := range []string{`<th class="hide-md">Outbound auth</th>`, `class="hide-md trunc"`} {
		if !strings.Contains(ups, want) {
			t.Errorf("upstreams page lacks %q", want)
		}
	}
	// the table has no Target or Host override column and no subtitle under the alias: the alias box's hover text
	// carries the type, target, source and host override
	for _, gone := range []string{"<th>Target</th>", "Host override</th>", `class="sub"`, "re-detect"} {
		if strings.Contains(ups, gone) {
			t.Errorf("upstreams page still has %q", gone)
		}
	}
	for _, want := range []string{"type: remote\nurl: " + up.URL + "/a/very/long/path", "type: stdio\ncommand: " + os.Args[0], "type: git\nsource: https://git.example.com/org/repo.git\ncommand: python3 -m srv", ">detect</button>"} {
		if !strings.Contains(ups, want) {
			t.Errorf("upstreams page lacks %q", want)
		}
	}
	// managed rows use the same pill, chip and action components as remote ones
	for _, want := range []string{`class="pill off" title="`, `>stopped</span>`, `href="/admin/upstreams/logs?alias=mgd"`, `href="/admin/upstreams/import"`, `href="/admin/upstreams/export"`} {
		if !strings.Contains(ups, want) {
			t.Errorf("upstreams page lacks %q", want)
		}
	}
	// the same form partial is used by the add form and both edit pages
	for _, name := range []string{"/admin/upstreams", "/admin/upstreams/edit?alias=one", "/admin/upstreams/edit?alias=mgd", "/admin/upstreams/edit?alias=repo"} {
		for _, want := range []string{`class="group" data-kind="remote"`, `class="group" data-kind="stdio git"`, `data-kind-select`, `class="pairs"`, `data-pairs-add`, `data-pairs-template`} {
			if !strings.Contains(pages[name], want) {
				t.Errorf("%s lacks the shared form component %q", name, want)
			}
		}
	}
	// nothing about trailing slashes is ever shown
	for name, page := range pages {
		if strings.Contains(strings.ToLower(page), "slash") {
			t.Errorf("%s mentions trailing slashes", name)
		}
	}
	// secrets are masked everywhere, never rendered
	for name, page := range pages {
		for _, secret := range []string{"env-secret-value-9876", "git-secret-token-5555", "sekret-value-1234"} {
			if strings.Contains(page, secret) {
				t.Errorf("%s renders the secret %q", name, secret)
			}
		}
	}
	if !strings.Contains(pages["/admin/upstreams/edit?alias=mgd"], "************9876") || !strings.Contains(pages["/admin/upstreams/edit?alias=repo"], "************5555") {
		t.Error("stored secrets must show as 12 asterisks plus the last 4 characters")
	}
	if !strings.Contains(pages["/admin/keys"], `class="pill ok">active`) || !strings.Contains(pages["/admin/clients"], `class="card`) {
		t.Error("keys and clients pages must use the shared pill and card components")
	}
	// the stylesheet defines every shared component the templates use
	r, _ = http.Get(ts.URL + "/admin/static/app.css")
	cssb, _ := io.ReadAll(r.Body)
	r.Body.Close()
	css := string(cssb)
	used := map[string]bool{}
	for _, page := range pages {
		for _, m := range regexp.MustCompile(`class="([^"]+)"`).FindAllStringSubmatch(page, -1) {
			for _, c := range strings.Fields(m[1]) {
				used[c] = true
			}
		}
	}
	for c := range used {
		if c == "on" {
			continue
		}
		if !strings.Contains(css, "."+c) {
			t.Errorf("class %q is used by a template but has no rule in app.css", c)
		}
	}
	for _, want := range []string{"white-space:nowrap", "text-overflow:ellipsis", "overflow-x:auto", "@media", "header form:last-child{margin-right:", "max-width:1600px", ".hide-md{display:none}", ".hide-sm{display:none}", "td.trunc{width:32%;max-width:0"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %q", want)
		}
	}
}

// The admin CSP forbids inline scripts, so templates must not carry inline event handlers.
func TestNoInlineEventHandlers(t *testing.T) {
	a, _, br, csrf := signedIn(t, func(c *config.Config, _ *oidctest.Provider) { c.ManagedDir = t.TempDir() })
	t.Cleanup(a.MCP.ShutdownManaged)
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "mgd", Kind: mcp.KindStdio, Command: os.Args[0], Enabled: true})
	up := fakeUpstream(t)
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "one", URL: up.URL, AuthKind: mcp.AuthNone, Enabled: true})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "redirects": {"https://c.example/cb"}, "method": {"none"}})
	re := regexp.MustCompile(`(?i)\son[a-z]+=|javascript:|<script[^>]*>[^<]`)
	for _, p := range []string{"/admin", "/admin/keys", "/admin/upstreams", "/admin/upstreams/edit?alias=one", "/admin/upstreams/import", "/admin/upstreams/logs?alias=mgd", "/admin/clients"} {
		_, page := br.get(p)
		if re.MatchString(page) {
			t.Errorf("%s has an inline handler or script: %q", p, re.FindString(page))
		}
	}
	_, ups := br.get("/admin/upstreams")
	if !strings.Contains(ups, `data-confirm="`) {
		t.Error("destructive forms must use data-confirm")
	}
}

// Admin copy is terse: no hand-holding sentences, and no long muted paragraphs.
func TestAdminCopyIsTerse(t *testing.T) {
	a, _, br, csrf := signedIn(t, func(c *config.Config, _ *oidctest.Provider) { c.ManagedDir = t.TempDir() })
	t.Cleanup(a.MCP.ShutdownManaged)
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "mgd", Kind: mcp.KindStdio, Command: os.Args[0], Enabled: true})
	up := fakeUpstream(t)
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "one", URL: up.URL, AuthKind: mcp.AuthNone, Enabled: true})
	fluff := []string{"no keys yet", "no upstreams yet", "no clients yet", "learned automatically", "shown once, copy it now", "stored, later shown",
		"Nothing is retried", "You are signed out", "leave empty to keep", "e.g. ", "This page updates by itself", "normally served by"}
	p := regexp.MustCompile(`(?s)<p class="muted">(.*?)</p>`)
	for _, path := range []string{"/admin", "/admin/keys", "/admin/upstreams", "/admin/upstreams/edit?alias=one", "/admin/upstreams/edit?alias=mgd", "/admin/upstreams/import", "/admin/upstreams/logs?alias=mgd", "/admin/clients"} {
		_, page := br.get(path)
		for _, f := range fluff {
			if strings.Contains(page, f) {
				t.Errorf("%s: fluff %q", path, f)
			}
		}
		for _, m := range p.FindAllStringSubmatch(page, -1) {
			if txt := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[1], ""); len(txt) > 200 {
				t.Errorf("%s: %d-char hint is too long: %.60s...", path, len(txt), txt)
			}
		}
	}
	_ = csrf
}
