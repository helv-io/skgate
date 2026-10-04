package app

import (
	"net/url"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/mcp"
)

func TestUpstreamToggleButtons(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "t1", URL: up.URL, AuthKind: mcp.AuthBearer, AuthName: "", AuthValue: "keepme-1234", HostOverride: "h.example:81", Enabled: true, IncludeInMCP: false})
	_, page := br.get("/admin/upstreams")
	if !strings.Contains(page, `<button class="act toggle on" role="switch" aria-checked="true" title="toggle enabled">Enabled</button>`) ||
		!strings.Contains(page, `<button class="act toggle off" role="switch" aria-checked="false" title="toggle in /mcp">Not in /mcp</button>`) {
		t.Fatalf("toggle buttons missing:\n%s", page)
	}
	// POST + CSRF only
	if r, _ := br.get("/admin/upstreams/t1/toggle?flag=include"); r.StatusCode != 405 {
		t.Errorf("GET: %d", r.StatusCode)
	}
	if r, _ := br.post("/admin/upstreams/t1/toggle", url.Values{"flag": {"include"}}); r.StatusCode != 403 {
		t.Errorf("no csrf: %d", r.StatusCode)
	}
	r, _ := br.post("/admin/upstreams/t1/toggle", url.Values{"csrf": {csrf}, "flag": {"include"}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/upstreams" {
		t.Fatalf("toggle include: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if k, m := flashOf(r); k != "ok" || m != "t1: in /mcp on" {
		t.Fatalf("toast %q %q", k, m)
	}
	u, _ := a.MCP.Upstreams.Get("t1")
	if !u.IncludeInMCP || !u.Enabled || u.AuthValue != "keepme-1234" || u.HostOverride != "h.example:81" || u.AuthKind != mcp.AuthBearer {
		t.Fatalf("toggle must change only the flag: %+v", u)
	}
	_, page = br.get("/admin/upstreams")
	if !strings.Contains(page, `class="act toggle on" role="switch" aria-checked="true" title="toggle in /mcp">In /mcp<`) {
		t.Error("included must render as an 'on' switch")
	}
	r, _ = br.post("/admin/upstreams/t1/toggle", url.Values{"csrf": {csrf}, "flag": {"enabled"}})
	if _, m := flashOf(r); m != "t1: enabled off" {
		t.Fatalf("toast %q", m)
	}
	u, _ = a.MCP.Upstreams.Get("t1")
	if u.Enabled || !u.IncludeInMCP {
		t.Fatalf("%+v", u)
	}
	_, page = br.get("/admin/upstreams")
	if !strings.Contains(page, `class="act toggle off" role="switch" aria-checked="false" title="toggle enabled">Disabled<`) {
		t.Error("disabled must render as an 'off' switch")
	}
	for _, v := range []url.Values{{"alias": {"nope"}, "flag": {"enabled"}}, {"alias": {"t1"}, "flag": {"x"}}} {
		v.Set("csrf", csrf)
		if r, _ := br.post("/admin/upstreams/"+v.Get("alias")+"/toggle", v); r.StatusCode != 303 || flashKind(r) != "bad" {
			t.Errorf("%v: %d %q", v, r.StatusCode, flashKind(r))
		}
	}
}
