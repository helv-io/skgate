package app

import (
	"github.com/helv-io/skgate/internal/mcp"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	_, page := br.get("/admin/upstreams/new")
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

// Destructive buttons (delete, revoke, stop, clear logs) are red at rest, not only on hover: touch screens
// have no hover, and a dim button reads as disabled.
func TestDangerButtonsAreRedAtRest(t *testing.T) {
	css := appCSS(t)
	i := strings.Index(css, ".act.danger,button.danger{")
	if i < 0 {
		t.Fatal("no danger rule")
	}
	rule := css[i : i+strings.Index(css[i:], "}")]
	if !strings.Contains(rule, "color:var(--bad)") || !strings.Contains(rule, "border-color:") || strings.Contains(rule, "var(--dim)") {
		t.Errorf("the resting danger rule must be red: %s", rule)
	}
}

// Readable text on large screens: 15 px body, 13 px section headings, content no wider than 1200 px (the status
// card used to stretch 1600 px, far from what it describes).
func TestBaseTypeAndContentWidth(t *testing.T) {
	css := appCSS(t)
	for _, want := range []string{"font:15px/1.5 var(--sans)", "main{max-width:1200px;", "h2{font-size:13px;"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
}

// Every form control on every admin page, in dialogs and row templates too, has a name a screen reader announces
// (a wrapping label, aria-label...), not just "edit text".
func TestEveryFormControlHasAName(t *testing.T) {
	node, err := exec.LookPath("node")
	jsdom := os.Getenv("SKGATE_JSDOM")
	if err != nil || jsdom == "" {
		t.Skip("set SKGATE_JSDOM (and install node) to run the label check")
	}
	up, _ := aliasUpstream(t)
	_, _, br, csrf, _ := signedInProvider(t, up)
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}, "expires": {"30d"}})
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"r"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"header"}, "auth_name": {"X-K"}, "auth_value": {"secret-value-1234"}, "hdr_name": {"X-A"}, "hdr_value": {"1"}})
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"n"}, "redirects": {"https://x.example/cb"}, "method": {"client_secret_post"}})
	dir := t.TempDir()
	for file, path := range map[string]string{"status.html": "/admin", "keys.html": "/admin/keys", "upstreams.html": "/admin/upstreams", "edit-remote.html": "/admin/upstreams/edit?alias=r",
		"import.html": "/admin/upstreams/import", "clients.html": "/admin/clients"} {
		_, page := br.get(path)
		if err := os.WriteFile(filepath.Join(dir, file), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// a managed upstream has fields of its own (source, command, args, environment, install)
	_, mbr, mcsrf := managedApp(t)
	mbr.post("/admin/upstreams/save", stdioForm(mcsrf, "m", url.Values{"lifecycle": {"always"}, "args": {"-x", "-y"}}))
	_, page := mbr.get("/admin/upstreams/m/edit")
	os.WriteFile(filepath.Join(dir, "edit-managed.html"), []byte(page), 0o600)
	_, page = mbr.get("/admin/upstreams/new")
	os.WriteFile(filepath.Join(dir, "managed-list.html"), []byte(page), 0o600)
	script, _ := filepath.Abs(filepath.Join("testdata", "labels.js"))
	cmd := exec.Command(node, script, dir)
	cmd.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(jsdom, "node_modules"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// The Add upstream form follows the work: Type, Source, Suggest, its results, then the alias (which Suggest
// fills), then the rest. Alias used to come first and, being required, sent people back up after Suggest.
func TestAddUpstreamFieldOrderFollowsSuggest(t *testing.T) {
	_, br, _ := managedApp(t)
	_, page := br.get("/admin/upstreams/new")
	form := page[strings.Index(page, `action="/admin/upstreams/save"`):]
	last := -1
	for _, m := range []string{`name="kind"`, `name="source"`, `data-suggest="/admin/upstreams/suggest"`, `data-suggest-out`, `name="alias" required`, `name="url"`, `name="command_pick"`} {
		i := strings.Index(form, m)
		if i < 0 {
			t.Fatalf("no %s", m)
		}
		if i < last {
			t.Errorf("%s comes too early in the form", m)
		}
		last = i
	}
}

// The add-upstream form tells the page script which fields belong to which kind of source, and that Suggest is
// off for a reason the server knows (no helper model).
func TestAddUpstreamFieldsDeclareTheirKind(t *testing.T) {
	_, br, _ := managedApp(t)
	_, page := br.get("/admin/upstreams/new")
	form := page[strings.Index(page, `action="/admin/upstreams/save"`):]
	for _, want := range []string{
		`data-show-for="git"><label>Ref`, `data-show-for="git"><label>Access token`,
		`data-hide-for="package"><label>Install command`,
		`data-suggest-off`, `aria-live="polite"`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the form lacks %s", want)
		}
	}
}

// Adding an upstream has its own page; the list only links to it. A refused save returns to the form it came from.
func TestAddUpstreamHasItsOwnPage(t *testing.T) {
	_, br, csrf := managedApp(t)
	_, list := br.get("/admin/upstreams")
	if strings.Contains(list, `action="/admin/upstreams/save"`) || strings.Contains(list, `data-kind-select`) {
		t.Error("the list must not carry the add form")
	}
	if !strings.Contains(list, `<a class="btn" href="/admin/upstreams/new">Add upstream</a>`) {
		t.Error("the list lacks the primary Add upstream button")
	}
	resp, page := br.get("/admin/upstreams/new")
	if resp.StatusCode != 200 || !strings.Contains(page, "<h2>Add upstream</h2>") || !strings.Contains(page, `name="mode" value="new"`) ||
		!strings.Contains(page, `action="/admin/upstreams/save"`) || !strings.Contains(page, `<a class="act" href="/admin/upstreams">cancel</a>`) {
		t.Errorf("the add page is incomplete (%d)", resp.StatusCode)
	}
	resp, _ = br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"bad alias!"}, "url": {"http://x.example/mcp"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/upstreams/new" {
		t.Errorf("a refused add goes back to the form: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"ok"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"none"}})
	resp, _ = br.post("/admin/upstreams/ok/save", url.Values{"csrf": {csrf}, "kind": {"remote"}, "url": {"not a url"}, "auth_kind": {"none"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/upstreams/ok/edit" {
		t.Errorf("a refused edit goes back to its form: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// The list says what an upstream is (Type) and how it is doing (Status) in separate columns, with one vocabulary:
// remote, managed · git, managed · npm, managed · pypi, managed · command. The name opens the details.
func TestUpstreamListSplitsTypeAndStatus(t *testing.T) {
	a, br, _ := managedApp(t)
	for _, u := range []mcp.Upstream{
		{Alias: "rem", URL: "http://127.0.0.1:1/mcp", AuthKind: mcp.AuthNone, Enabled: true},
		{Alias: "gitsrv", Kind: mcp.KindGit, Command: "python3", GitURL: "https://git.example.com/org/repo.git", Enabled: true},
		{Alias: "nodepkg", Kind: mcp.KindStdio, Command: "npx", Args: []string{"-y", "thing@1.0.0"}, Enabled: true},
		{Alias: "pypkg", Kind: mcp.KindStdio, Command: "uvx", Args: []string{"thing==1.0.0"}, Enabled: true},
		{Alias: "prog", Kind: mcp.KindStdio, Command: "cat", Enabled: true},
	} {
		if err := a.MCP.Upstreams.Create(u); err != nil {
			t.Fatal(err)
		}
	}
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, `<th>Status</th><th>Name</th><th>Type</th><th>Enabled</th>`) {
		t.Error("the columns are Status, Name, Type, Enabled, ...")
	}
	row := func(alias string) string {
		for _, r := range strings.Split(list, "<tr>")[1:] {
			if i := strings.Index(r, "</tr>"); i >= 0 && strings.Contains(r[:i], `href="#upstream-`+alias+`"`) {
				return r[:i]
			}
		}
		t.Fatalf("no row for %s", alias)
		return ""
	}
	for alias, want := range map[string]string{"rem": "remote", "gitsrv": "managed \u00b7 git", "nodepkg": "managed \u00b7 npm", "pypkg": "managed \u00b7 pypi", "prog": "managed \u00b7 command"} {
		r := row(alias)
		if !strings.Contains(r, `<td class="inline" data-label="Type"><span class="chip">`+want+`</span></td>`) {
			t.Errorf("%s: type is not %q:\n%s", alias, want, r)
		}
		if !strings.Contains(r, `data-label="Status"`) || !strings.Contains(r, `<a class="name" href="#upstream-`+alias+`" data-dialog-open="#upstream-`+alias+`"`) {
			t.Errorf("%s: no status cell or the name does not open the details", alias)
		}
		st := r[strings.Index(r, `data-label="Status"`):]
		st = st[:strings.Index(st, "</td>")]
		if strings.Contains(st, want) || strings.Contains(st, "managed") {
			t.Errorf("%s: the type is repeated in the status cell: %s", alias, st)
		}
	}
	if st := row("rem"); !strings.Contains(st, ">no calls yet</span>") {
		t.Error("a remote upstream nobody called yet says so in its status")
	}
	if st := row("prog"); !strings.Contains(st, ">stopped</span>") {
		t.Error("a managed upstream shows its process state in the status")
	}
	_, edit := br.get("/admin/upstreams/nodepkg/edit")
	if !strings.Contains(edit, "<span class=\"chip\">managed \u00b7 npm</span>") {
		t.Error("the edit page uses the same name for the type")
	}
}

// The endpoints are at the top of the page and the toolbar (Add upstream, import, export) sits above the table.
func TestUpstreamListPageOrder(t *testing.T) {
	_, br, _ := managedApp(t)
	_, page := br.get("/admin/upstreams")
	last := -1
	for _, m := range []string{"<h2>MCP upstreams</h2>", ".well-known/<wbr>oauth-authorization-server", ">Add upstream</a>", `href="/admin/upstreams/import"`, `href="/admin/upstreams/export"`, "<thead>"} {
		i := strings.Index(page, m)
		if i < 0 || i < last {
			t.Fatalf("%q is missing or out of order on the list page", m)
		}
		last = i
	}
	if strings.Contains(page[strings.Index(page, "</table></div>\n"):], "Endpoints") || strings.Contains(page, "<h2>Endpoints</h2>") {
		t.Error("the endpoints are still at the bottom")
	}
}
