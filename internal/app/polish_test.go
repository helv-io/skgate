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
