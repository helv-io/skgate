package app

import (
	"github.com/helv-io/skgate/internal/mcp"
	"net/url"
	"strings"
	"testing"
)

func TestUpstreamTestButton(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"fake"}, "url": {up.URL}, "auth_kind": {"auto"}, "enabled": {"1"}})
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, "/admin/upstreams/test") {
		t.Fatal("list must have the Test action")
	}
	path := "/admin/upstreams/test"
	if r, _ := br.post(path, url.Values{"alias": {"fake"}}); r.StatusCode != 403 {
		t.Fatalf("%s without csrf: %d", path, r.StatusCode)
	}
	if r, _ := br.post(path, url.Values{"alias": {"fake"}, "csrf": {"wrong"}}); r.StatusCode != 403 {
		t.Fatalf("%s with bad csrf: %d", path, r.StatusCode)
	}
	if r, _ := br.get(path + "?alias=fake"); r.StatusCode != 405 {
		t.Fatalf("GET %s: %d", path, r.StatusCode)
	}
	r, body := br.post(path, url.Values{"alias": {"fake"}, "csrf": {csrf}})
	if r.StatusCode != 200 || !strings.Contains(body, "list_things") || !strings.Contains(body, "Lists things.") || strings.Contains(body, "More text.") ||
		!strings.Contains(body, "200") || !strings.Contains(body, "OK") || !strings.Contains(body, "Latency") || strings.Contains(body, "Trailing slash") {
		t.Fatalf("test page: %d\n%s", r.StatusCode, body)
	}
	// errors are shown plainly
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "dead", URL: "http://127.0.0.1:1/mcp", AuthKind: mcp.AuthNone, Enabled: true})
	_, body = br.post(path, url.Values{"alias": {"dead"}, "csrf": {csrf}})
	if !strings.Contains(body, "unreachable") || !strings.Contains(body, "failed") {
		t.Fatalf("error not shown: %s", body)
	}
}
