package app

import (
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
	_, _, br, csrf := signedIn(t, nil)
	host, port := deadHostPort(t)
	r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"nobody"}, "url": {"http://" + host + ":" + port + "/mcp"}, "auth_kind": {"none"}, "enabled": {"1"}})
	_, msg := flashOf(r)
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
