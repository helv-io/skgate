package app

import "testing"

// In a real browser: tabbing through the key form shows a ring on the rate field, the expiry field and each of
// the six quick buttons, on a phone and on a desktop (one shared :focus-visible rule).
func TestFocusRingsInBrowser(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	runBrowserScript(t, "focus.js", ts.URL, br)
}
