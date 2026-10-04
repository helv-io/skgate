package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// In a real browser: tabbing through the key form shows a ring on the rate field, the expiry field and each of
// the six quick buttons, on a phone and on a desktop (one shared :focus-visible rule).
func TestFocusRingsInBrowser(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	runBrowserScript(t, "focus.js", ts.URL, br)
}

// In a real browser: the upstream toggles are switches (role=switch, a track and a knob that moves), the "not in
// /mcp" state is neutral rather than red, and the switch that cannot be used (on-demand servers are never on /mcp)
// is visibly disabled.
func TestUpstreamSwitchesInBrowser(t *testing.T) {
	_, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "od", nil))
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"plain"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"none"}, "enabled": {"1"}})
	runBrowserScript(t, "switches.js", br.ts.URL, br)
}

// In a real browser: the Test screen leads with "OK · N tools · X ms", then the buttons, then the tools; each
// description is one line that opens to the whole text; the filter box narrows the list by name or description.
func TestUpstreamTestScreenInBrowser(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(b), `"initialize"`):
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"fake","version":"0.1"}}}`)
		case strings.Contains(string(b), `"tools/list"`):
			long := strings.Repeat("A very long first sentence that cannot fit on one line of a phone. ", 4)
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"alpha_tool","description":"Does alpha."},{"name":"beta_tool","description":"`+long+`\nThe second sentence is the rest."},{"name":"gamma_tool","description":"Feeds the kangaroo.\nMore."}]}}`)
		default:
			w.WriteHeader(202)
		}
	}))
	t.Cleanup(up.Close)
	_, ts, br, csrf := signedIn(t, nil)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"fake"}, "url": {up.URL}, "auth_kind": {"auto"}, "enabled": {"1"}})
	runBrowserScript(t, "testscreen.js", ts.URL, br)
}
