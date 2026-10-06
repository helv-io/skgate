package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/mcp"
)

func menuApp(t *testing.T) (*App, *browser) {
	a, br, _ := managedApp(t)
	for _, u := range []mcp.Upstream{
		{Alias: "alpha", URL: "http://127.0.0.1:1/mcp", AuthKind: mcp.AuthNone, Enabled: true},
		{Alias: "beta", Kind: mcp.KindStdio, Command: "cat", Enabled: true},
	} {
		if err := a.MCP.Upstreams.Create(u); err != nil {
			t.Fatal(err)
		}
	}
	return a, br
}

// A row keeps Copy URL and Test in view and puts Details, Edit and Delete in one ⋯ menu; the details dialog offers
// the same actions as the row, plus detection for a remote upstream. A managed row's process page is its status pill.
func TestUpstreamRowActionsAndMenuMarkup(t *testing.T) {
	_, br := menuApp(t)
	_, page := br.get("/admin/upstreams")
	row := func(alias string) string {
		for _, r := range strings.Split(strings.ReplaceAll(page, "<tr ", "<tr>"), "<tr>")[1:] {
			if i := strings.Index(r, "</tr>"); i >= 0 && strings.Contains(r[:i], `href="#upstream-`+alias+`"`) {
				return r[:i]
			}
		}
		t.Fatalf("no row %s", alias)
		return ""
	}
	r := row("alpha")
	cell := r[strings.Index(r, `<td class="actions-cell">`):]
	menuAt := strings.Index(cell, `data-menu>`)
	if menuAt < 0 {
		t.Fatal("no menu on the row")
	}
	visible, inMenu := cell[:menuAt], cell[menuAt:]
	for _, w := range []string{">Copy URL</button>", `href="/admin/upstreams/alpha/test"`} {
		if !strings.Contains(visible, w) {
			t.Errorf("the row must show %q outside the menu", w)
		}
	}
	for _, w := range []string{`data-dialog-open="#upstream-alpha"`, `href="/admin/upstreams/alpha/edit"`, `action="/admin/upstreams/alpha/delete"`} {
		if strings.Contains(visible, w) || !strings.Contains(inMenu, w) {
			t.Errorf("%q belongs in the menu only", w)
		}
	}
	if n := strings.Count(inMenu, `role="menuitem"`); n != 3 {
		t.Errorf("the menu has %d items, want 3", n)
	}
	for _, w := range []string{`aria-haspopup="menu"`, `aria-expanded="false"`, `aria-controls="menu-alpha"`, `id="menu-alpha" role="menu"`, `aria-label="More actions for alpha"`} {
		if !strings.Contains(inMenu+visible, w) {
			t.Errorf("menu markup lacks %q", w)
		}
	}
	// the delete form still asks first
	if !strings.Contains(inMenu, `data-confirm="Delete this upstream?"`) {
		t.Error("the delete in the menu has no confirmation")
	}
	// the details dialog: same actions as the row, plus detection for a remote upstream.
	// a managed upstream's process page is the status pill, not a Process button.
	dlg := func(alias string) string {
		i := strings.Index(page, `id="upstream-`+alias+`"`)
		return page[i : i+strings.Index(page[i:], "</template>")]
	}
	d := dlg("alpha")
	acts := d[:strings.Index(d, "<h4>")]
	for _, w := range []string{">Copy URL</button>", `href="/admin/upstreams/alpha/test"`, `href="/admin/upstreams/alpha/edit"`, `action="/admin/upstreams/alpha/delete"`, ">Detect</button>"} {
		if !strings.Contains(acts, w) {
			t.Errorf("the alpha dialog lacks %q among its actions", w)
		}
	}
	d = dlg("beta")
	acts = d[:strings.Index(d, "<h4>")]
	for _, w := range []string{">Copy URL</button>", `href="/admin/upstreams/beta/test"`, `href="/admin/upstreams/beta/edit"`, `action="/admin/upstreams/beta/delete"`} {
		if !strings.Contains(acts, w) {
			t.Errorf("the beta dialog lacks %q among its actions", w)
		}
	}
	if strings.Contains(acts, ">Process</a>") || strings.Contains(acts, "/beta/logs") {
		t.Error("the dialog must not offer a separate Process action")
	}
	if !strings.Contains(row("beta"), `href="/admin/upstreams/beta/logs"`) || !strings.Contains(row("beta"), ">stopped</a>") {
		t.Error("the status pill must link to the process page")
	}
}

// The ⋯ menu in jsdom: opening, arrow keys, Escape, outside click, Tab, one at a time. Needs node and jsdom.
func TestRowMenuInJSDOM(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	env := os.Environ()
	if d := os.Getenv("SKGATE_JSDOM"); d != "" {
		env = append(env, "NODE_PATH="+filepath.Join(d, "node_modules"))
	}
	probe := exec.Command(node, "-e", `require("jsdom")`)
	probe.Env = env
	if probe.Run() != nil {
		t.Skip("jsdom is not available (set SKGATE_JSDOM to a directory with node_modules/jsdom)")
	}
	_, br := menuApp(t)
	_, page := br.get("/admin/upstreams")
	dir := t.TempDir()
	file := filepath.Join(dir, "list.html")
	if err := os.WriteFile(file, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "menu.js"))
	cmd := exec.Command(node, script, file, js)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// In Chrome (optional, like the layout sweep): the menu is not clipped by the scrolling table, Escape and outside
// clicks close it, Delete still asks and Cancel returns to the button, Details and the name open the dialog, and on
// a phone a tap opens it and the items are tap-sized.
func TestRowMenuInBrowser(t *testing.T) {
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the menu check in a browser")
	}
	a, br := menuApp(t)
	_ = a
	ts := br.ts
	script, _ := filepath.Abs(filepath.Join("testdata", "menubrowser.js"))
	out, err := exec.Command(node, script, ts.URL, chrome, filepath.Join(pp, "node_modules", "puppeteer-core"), browserCookies(br, ts.URL)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}
