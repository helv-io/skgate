package app

import (
	"github.com/helv-io/skgate/internal/mcp"
	"net/url"
	"strings"
	"testing"
)

func TestUpstreamAutoModeAndRedetect(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	_, form := br.get("/admin/upstreams/new")
	if !strings.Contains(form, `<option value="auto" selected>`) {
		t.Fatal("auto must be the default for new upstreams")
	}
	r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"fake"}, "url": {up.URL}, "auth_kind": {"auto"}, "enabled": {"1"}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/upstreams" {
		t.Fatalf("save should redirect cleanly: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if k, m := flashOf(r); k != "ok" || !strings.Contains(m, "detected: none") {
		t.Fatalf("save should detect immediately: %q %q", k, m)
	}
	u, _ := a.MCP.Upstreams.Get("fake")
	if u.AuthKind != mcp.AuthAuto || u.DetectedKind != "none" {
		t.Fatalf("stored: %+v", u)
	}
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, "auto: none") || !strings.Contains(list, "/admin/upstreams/fake/redetect") {
		t.Fatal("list must show the detected kind and the re-detect action")
	}
	path := "/admin/upstreams/fake/redetect"
	if r, _ := br.post(path, url.Values{"alias": {"fake"}}); r.StatusCode != 403 {
		t.Fatalf("%s without csrf: %d", path, r.StatusCode)
	}
	if r, _ := br.post(path, url.Values{"alias": {"fake"}, "csrf": {"wrong"}}); r.StatusCode != 403 {
		t.Fatalf("%s with bad csrf: %d", path, r.StatusCode)
	}
	if r, _ := br.get(path); r.StatusCode != 405 {
		t.Fatalf("GET %s: %d", path, r.StatusCode)
	}
	r, _ = br.post(path, url.Values{"alias": {"fake"}, "csrf": {csrf}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/upstreams" {
		t.Fatalf("redetect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if _, m := flashOf(r); !strings.Contains(m, "detected none") {
		t.Fatalf("redetect toast: %q", m)
	}
	// manual override still possible and detection not applied to existing manual rows
	r, _ = br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"edit"}, "alias": {"fake"}, "url": {up.URL}, "auth_kind": {"passthrough"}, "enabled": {"1"}})
	if r.StatusCode != 303 || flashKind(r) == "bad" {
		t.Fatalf("manual override: %s", r.Header.Get("Location"))
	}
	if u, _ := a.MCP.Upstreams.Get("fake"); u.AuthKind != mcp.AuthPassthrough || u.DetectedKind != "" {
		t.Fatalf("override: %+v", u)
	}
}
