package app

import (
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func appCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../admin/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The page keeps room for the scrollbar, so opening a dialog does not move everything sideways; an alias in a
// heading keeps its case (headings are uppercase, aliases are case-sensitive).
func TestPageDoesNotShiftAndAliasesKeepTheirCase(t *testing.T) {
	css := appCSS(t)
	for _, want := range []string{"html{scrollbar-gutter:stable}", "h2 code{text-transform:none;letter-spacing:0}"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
}

// Chrome compiles pattern= with the v flag, where an unescaped '-' at the end of a class is a syntax error and the
// check silently does not run. The alias pattern must compile under both flags.
func TestAliasPatternCompilesWithTheVFlag(t *testing.T) {
	_, br, _ := managedApp(t)
	_, page := br.get("/admin/upstreams")
	pat := between(page, `name="alias" required pattern="`, `"`)
	if pat != `[a-z0-9\-]+` {
		t.Fatalf("alias pattern %q", pat)
	}
	if node, err := exec.LookPath("node"); err == nil {
		out, err := exec.Command(node, "-e", `for (const f of ["u","v"]) new RegExp("^(?:"+process.argv[1]+")$", f)`, pat).CombinedOutput()
		if err != nil {
			t.Fatalf("the pattern does not compile: %s", out)
		}
	}
}

// A managed upstream has a lifecycle, not an outbound auth: its Details dialog must not show the lifecycle under that name.
func TestManagedUpstreamHasNoOutboundAuthRow(t *testing.T) {
	_, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", url.Values{"lifecycle": {"always"}}))
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"r"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"none"}})
	_, page := br.get("/admin/upstreams")
	dlg := func(alias string) string {
		i := strings.Index(page, `id="upstream-`+alias+`"`)
		if i < 0 {
			t.Fatalf("no dialog for %s", alias)
		}
		j := strings.Index(page[i:], "</template>")
		return page[i : i+j]
	}
	if strings.Contains(dlg("m"), "outbound auth") || !strings.Contains(dlg("m"), "<th>lifecycle</th>") {
		t.Errorf("managed dialog:\n%s", dlg("m"))
	}
	if !strings.Contains(dlg("r"), "<th>outbound auth</th>") {
		t.Error("a remote upstream shows its outbound auth")
	}
}

// An address that does not exist, and an authorization request that cannot go on, answer in the admin design
// (the Solo layout: brand mark, a sentence, a link back) with the right status, not as a bare line of text.
func TestErrorPagesAreBranded(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	check := func(name, path string, status int, want ...string) {
		t.Helper()
		resp, body := br.get(path)
		if resp.StatusCode != status {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, status)
		}
		for _, w := range append(want, `class="brandmark"`, `<a class="btn" href="/admin">Back to skgate</a>`, "/admin/static/app.css") {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q:\n%s", name, w, body)
			}
		}
		if strings.Contains(body, "style=") || strings.Contains(body, "404 page not found") {
			t.Errorf("%s is not in the admin design:\n%s", name, body)
		}
	}
	check("404", "/admin/nothing-here", 404, "Page not found", "There is no such page")
	check("bad alias", "/admin/upstreams/BAD_ALIAS/test", 404, "Page not found")
	check("unknown client", "/authorize?response_type=code&client_id=nope&redirect_uri=https://x.example/cb&code_challenge=abc&code_challenge_method=S256",
		400, "Authorization error", "not registered with skgate")
	// a signed-out visitor gets the same page (it reveals nothing)
	resp, body := newBrowser(t, ts).get("/admin/nothing-here")
	if resp.StatusCode != 404 || !strings.Contains(body, "Page not found") {
		t.Errorf("signed out: %d %s", resp.StatusCode, body)
	}
}
