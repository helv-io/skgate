package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/mcp"
)

func TestUpstreamTestButton(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"fake"}, "url": {up.URL}, "auth_kind": {"auto"}, "enabled": {"1"}})
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, `href="/admin/upstreams/fake/test"`) {
		t.Fatal("list must have the Test action, a plain link")
	}
	// a screen: GET only, refreshable, no CSRF needed; anything that is not GET is refused
	path := "/admin/upstreams/fake/test"
	if r, _ := br.post(path, url.Values{"csrf": {csrf}}); r.StatusCode != 405 {
		t.Fatalf("POST %s: %d", path, r.StatusCode)
	}
	anon := newBrowser(t, br.ts)
	if r, _ := anon.get(path); r.StatusCode != 302 && r.StatusCode != 303 {
		t.Fatalf("%s without a session: %d", path, r.StatusCode)
	}
	for _, bad := range []string{"/admin/upstreams/UP/test", "/admin/upstreams/a_b/test", "/admin/upstreams/" + strings.Repeat("a", 64) + "/test"} {
		if r, _ := br.get(bad); r.StatusCode != 404 {
			t.Errorf("%s is not an alias: %d", bad, r.StatusCode)
		}
	}
	for i := 0; i < 2; i++ { // a refresh shows the same screen
		r, body := br.get(path)
		if r.StatusCode != 200 || !strings.Contains(body, "list_things") || !strings.Contains(body, "<summary>Lists things.</summary><p>More text.</p>") ||
			!strings.Contains(body, "200") || !strings.Contains(body, "OK \u00b7 1 tool \u00b7 ") || !strings.Contains(body, "Latency") || strings.Contains(body, "Trailing slash") {
			t.Fatalf("test page: %d\n%s", r.StatusCode, body)
		}
		for _, want := range []string{`data-dialog-open="#try-0">Test</button>`, `id="try-0"`, "data-try-run", "Run calls the upstream for real.", "/admin/upstreams/fake/tools/try/run"} {
			if !strings.Contains(body, want) {
				t.Errorf("test page lacks the tool tester %q", want)
			}
		}
		// Test is its own column on the right. The name cell is the tool name only, so the buttons line up.
		for _, want := range []string{
			`<th class="actions-th">Actions</th>`,
			`<td class="primary" data-label="Name"><code>list_things</code></td>`,
			`<td class="actions-cell"><div class="actions"><button type="button" class="act" data-dialog-open="#try-0">Test</button></div></td>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("tools table: Test must sit in the actions column, not beside the name; missing %q", want)
			}
		}
		// the summary, then the buttons, then the tools: the actions never wait below a long list
		i, j, k := strings.Index(body, "OK \u00b7 1 tool"), strings.Index(body, `>Test again</a>`), strings.Index(body, "<h2>Tools")
		if !(i > 0 && i < j && j < k) || !strings.Contains(body, `data-filter="#tools-table"`) {
			t.Errorf("order must be summary, actions, tools with a filter box: %d %d %d", i, j, k)
		}
		if !regexp.MustCompile(`<th>Latency</th><td>\d+(\.\d)? m?s</td>`).MatchString(body) {
			t.Errorf("latency is rounded for people (435 ms, 5.8 ms):\n%s", body)
		}
	}
	// errors are shown plainly
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "dead", URL: "http://127.0.0.1:1/mcp", AuthKind: mcp.AuthNone, Enabled: true})
	_, body := br.get("/admin/upstreams/dead/test")
	if !strings.Contains(body, "unreachable") || !strings.Contains(body, "failed \u00b7 ") {
		t.Fatalf("error not shown: %s", body)
	}
	// an unknown alias goes back to the list with a message
	if r, _ := br.get("/admin/upstreams/nope/test"); r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Errorf("unknown alias: %d %q", r.StatusCode, flashKind(r))
	}
}

// The Auth row is the scheme that was used, once. A stored detection must not repeat it
// ("bearer" next to "bearer, found bearer").
func TestUpstreamTestAuthShownOnce(t *testing.T) {
	a, _, br, _ := signedIn(t, nil)
	open := fakeUpstream(t)
	strict := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1234" && r.Header.Get("X-Api-Key") != "tok-1234" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(b), `"tools/list"`):
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`)
		case strings.Contains(string(b), `"initialize"`):
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"fake","version":"1"}}}`)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(strict.Close)
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(refused.Close)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.MCP.Upstreams.Create(mcp.Upstream{Alias: "set", URL: open.URL, AuthKind: mcp.AuthBearer, AuthValue: "tok-1234", Enabled: true}))
	must(a.MCP.Upstreams.SetDetected("set", "bearer", "bearer: HTTP 200, valid initialize result"))
	must(a.MCP.Upstreams.Create(mcp.Upstream{Alias: "auto", URL: strict.URL, AuthKind: mcp.AuthAuto, AuthValue: "tok-1234", Enabled: true}))
	must(a.MCP.Upstreams.Create(mcp.Upstream{Alias: "hdr", URL: strict.URL, AuthKind: mcp.AuthHeader, AuthName: "X-Api-Key", AuthValue: "tok-1234", Enabled: true}))
	must(a.MCP.Upstreams.Create(mcp.Upstream{Alias: "plain", URL: open.URL, AuthKind: mcp.AuthNone, Enabled: true}))
	must(a.MCP.Upstreams.Create(mcp.Upstream{Alias: "miss", URL: refused.URL, AuthKind: mcp.AuthAuto, AuthValue: "tok-1234", Enabled: true}))

	cell := regexp.MustCompile(`<th>Auth</th><td>(.*?)</td>`)
	for _, c := range []struct{ alias, want string }{
		{"set", "bearer"},
		{"auto", "bearer"},
		{"hdr", "header"},
		{"plain", "none"},
		{"miss", "none"},
	} {
		_, body := br.get("/admin/upstreams/" + c.alias + "/test")
		m := cell.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("%s: no Auth row:\n%s", c.alias, body)
		}
		if m[1] != c.want || strings.Contains(m[1], "found") || strings.Contains(m[1], "chip") {
			t.Errorf("%s: Auth cell %q, want %q", c.alias, m[1], c.want)
		}
	}
}
