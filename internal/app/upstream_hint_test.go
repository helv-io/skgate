package app

import (
	"github.com/helv-io/skgate/internal/mcp"
	"net"
	"net/url"
	"strings"
	"testing"
)

func deadHostPort(t *testing.T) (host, port string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	host, port, _ = net.SplitHostPort(l.Addr().String())
	return host, port
}

// A refused connection shows a hint with the address on the admin Test screen, and a short warning
// without the host when saving.
func TestRefusedUpstreamHints(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	host, port := deadHostPort(t)
	form := url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"nobody"}, "url": {"http://" + host + ":" + port + "/mcp"}, "auth_kind": {"none"}, "enabled": {"1"}}
	// the test before the save refuses it, in a plain sentence without the host, and stores nothing
	r, _ := br.post("/admin/upstreams/save", form)
	kind, msg := flashOf(r)
	if kind != "bad" || !strings.Contains(msg, "Nothing is listening on port "+port) || strings.Contains(msg, host) || !strings.HasSuffix(r.Header.Get("Location"), "/admin/upstreams/new") {
		t.Errorf("refusal: %q %q %s", kind, msg, r.Header.Get("Location"))
	}
	if _, ok := a.MCP.Upstreams.Get("nobody"); ok {
		t.Fatal("stored although the test failed")
	}
	a.Admin.NoSaveTest = true // keep it anyway, to see the Test screen of an upstream nothing answers at
	r, _ = br.post("/admin/upstreams/save", form)
	_, msg = flashOf(r)
	if !strings.Contains(msg, "saved") || !strings.Contains(msg, "Nothing is listening on port "+port) || strings.Contains(msg, host) {
		t.Errorf("save message: %q", msg)
	}
	_, body := br.get("/admin/upstreams/nobody/test")
	for _, want := range []string{"connection refused", "Hint", "Nothing is listening on port " + port, "Detail", host + ":" + port} {
		if !strings.Contains(body, want) {
			t.Errorf("test screen lacks %q:\n%s", want, body)
		}
	}
	// the list has the Test button on the row, and shows no address of the dead upstream in an error
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, `class="act" href="/admin/upstreams/nobody/test"`) {
		t.Errorf("the row needs a Test button")
	}
}

// Health and the last error appear on the list for remote upstreams only.
func TestUpstreamListShowsHealthOfRemoteOnly(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"fine"}, "url": {up.URL}, "auth_kind": {"none"}, "enabled": {"1"}})
	host, port := deadHostPort(t)
	a.Admin.NoSaveTest = true
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"broken"}, "url": {"http://" + host + ":" + port + "/mcp"}, "auth_kind": {"none"}, "enabled": {"1"}})
	br.get("/admin/upstreams/broken/test")
	br.get("/admin/upstreams/fine/test")
	_, list := br.get("/admin/upstreams")
	row := func(alias string) string {
		for _, r := range strings.Split(strings.ReplaceAll(list, "<tr ", "<tr>"), "<tr>")[1:] {
			if i := strings.Index(r, "</tr>"); i >= 0 && strings.Contains(r[:i], `href="#upstream-`+alias+`"`) {
				return r[:i]
			}
		}
		return ""
	}
	if r := row("fine"); !strings.Contains(r, ">ok</span>") || strings.Contains(r, "connection refused") {
		t.Errorf("healthy remote row: %s", r)
	}
	if r := row("broken"); !strings.Contains(r, ">failing</span>") || !strings.Contains(r, "connection refused") || strings.Contains(r, host+":"+port+"</") {
		t.Errorf("failing remote row: %s", r)
	}
	// a managed upstream shows its process state and no health
	if err := a.MCP.Upstreams.Create(mcp.Upstream{Alias: "proc", Kind: mcp.KindStdio, Command: "cat", Lifecycle: "on-demand"}); err != nil {
		t.Fatal(err)
	}
	_, list = br.get("/admin/upstreams")
	if r := row("proc"); r == "" || (strings.Contains(r, "failing") || strings.Contains(r, "no calls yet")) {
		t.Errorf("managed row shows health: %s", r)
	}
}
