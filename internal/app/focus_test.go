package app

import (
	"net/url"
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
