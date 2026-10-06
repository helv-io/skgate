package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// postJSON posts a form the way the page script does for a section that saves in place.
func postJSON(br *browser, path string, v url.Values) (*http.Response, string) {
	req, _ := http.NewRequest("POST", br.ts.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return br.do(req)
}

// A section saves in place: asked for JSON, every provider action answers with its toast and does not redirect, so
// the page (and what is typed in the other sections) stays where it is. Without JSON it redirects as before.
func TestProviderActionsAnswerInPlace(t *testing.T) {
	up, _ := aliasUpstream(t)
	a, _, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	for _, c := range []struct {
		path string
		form url.Values
		ok   bool
	}{
		{"/admin/providers/grok/settings", url.Values{"base": {up.URL + "/v1/"}, "fallback": {""}}, true},
		{"/admin/providers/grok/settings", url.Values{"base": {"nope"}}, false},
		{"/admin/providers/grok/aliases/put", url.Values{"name": {"fast"}, "target": {"grok-mini"}}, true},
		{"/admin/providers/grok/aliases/put", url.Values{"name": {"Bad Name"}, "target": {"grok-mini"}}, false},
		{"/admin/providers/grok/aliases/delete", url.Values{"name": {"fast"}}, true},
		{"/admin/providers/grok/browser/paste", url.Values{"callback": {"nonsense"}}, false},
	} {
		c.form.Set("csrf", csrf)
		resp, body := postJSON(br, c.path, c.form)
		var got struct {
			Toast struct{ K, M string }
		}
		if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &got) != nil || got.Toast.M == "" {
			t.Fatalf("%s: %d %q", c.path, resp.StatusCode, body)
		}
		if (got.Toast.K == "ok") != c.ok {
			t.Errorf("%s: toast %+v, want ok=%v", c.path, got.Toast, c.ok)
		}
	}
	if got := a.Admin.Set.Base(a.Providers.Default()); got != up.URL+"/v1" {
		t.Errorf("base saved as %q", got)
	}
	resp, _ := br.post("/admin/providers/grok/settings", url.Values{"csrf": {csrf}, "base": {up.URL + "/v1"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin#provider-grok" {
		t.Errorf("a plain post still returns to the dialog: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"grok-mini"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin#aliases-grok" {
		t.Errorf("an alias returns to the alias dialog: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// The helper model picker offers this provider's models, then its vendor aliases. A new alias points at the helper model.
func TestProviderDialogGroupsModelsAndDefaultsTheAliasTarget(t *testing.T) {
	up, _ := modelsUpstream(t, "grok-4", "grok-4-latest", "grok-mini", "grok-code-latest-fast")
	a, _, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"quick"}, "target": {"grok-mini"}})
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-mini"}})
	_, page := br.get("/admin")
	picker := page[strings.Index(page, `<select name="model"`):]
	picker = picker[:strings.Index(picker, "</select>")]
	last := -1
	for _, w := range []string{`<optgroup label="Models">`, `value="grok-4"`, `value="grok-mini"`,
		`<optgroup label="Vendor aliases">`, `value="grok-4-latest"`, `value="grok-code-latest-fast"`} {
		i := strings.Index(picker, w)
		if i < 0 || i < last {
			t.Fatalf("%q missing or out of order (%d after %d)", w, i, last)
		}
		last = i
	}
	if strings.Contains(picker, "Your aliases") || strings.Contains(picker, `value="quick"`) {
		t.Error("the helper picker lists aliases or another provider's models")
	}
	if strings.Index(picker, `<optgroup label="Models">`) > strings.Index(picker, `value="grok-4-latest"`) {
		t.Error("a vendor alias sits among the models")
	}
	if !strings.Contains(page, `<option value="grok-mini" selected>grok-mini</option>`) {
		t.Error("the target of a new alias does not default to the helper model")
	}
	for _, w := range []string{`<label>New alias<input`, `<label>Target model<select name="target"`} {
		if !strings.Contains(page, w) {
			t.Errorf("the alias row lacks %q", w)
		}
	}
	// an alias as the helper model: the new alias points at what it resolves to
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"quick"}})
	_, page = br.get("/admin")
	if !strings.Contains(page, `<option value="grok-mini" selected>grok-mini</option>`) {
		t.Error("with an alias as the helper model, the target is the alias' own target")
	}
	_ = a
}

// Details of every provider is the helper model, the aliases, and Remove. Sign-in and technical details are gone.
func TestProviderDialogSections(t *testing.T) {
	up, _ := modelsUpstream(t, "grok-4")
	_, _, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"grok-4"}})
	_, page := br.get("/admin")
	tpl := func(id string) string {
		i := strings.Index(page, `id="`+id+`"`)
		if i < 0 {
			t.Fatalf("missing dialog %s", id)
		}
		s := page[i:]
		return s[:strings.Index(s, "</template>")]
	}
	dlg := tpl("provider-grok")
	modelAt, aliasAt := strings.Index(dlg, `data-section="model"`), strings.Index(dlg, `data-section="aliases"`)
	if modelAt < 0 || aliasAt < 0 || modelAt > aliasAt {
		t.Fatal("helper model comes before aliases")
	}
	for _, gone := range []string{"Technical details", "Refresh now", "Browser sign-in", "Finish sign-in", "Sign in again",
		"Refresh token", "Danger zone", `data-section="signin"`, `data-section="technical"`, `data-section="upstream"`, "Upstream URLs", "Base URL", "Fallback"} {
		if strings.Contains(dlg, gone) {
			t.Errorf("Grok details still has %q", gone)
		}
	}
	if !strings.Contains(dlg, `data-confirm-title="Remove Grok?"`) || !strings.Contains(dlg, `data-confirm="Its aliases go too."`) || !strings.Contains(dlg, `data-confirm-ok="Remove"`) || !strings.Contains(dlg, `class="act danger">Remove provider</button>`) {
		t.Error("Remove provider is not the plain danger button with its confirmation")
	}
	if strings.Contains(dlg, "danger-zone") {
		t.Error("Remove sits in a danger zone")
	}
	// the remove form confirms, then posts. Every other form saves in place.
	for _, f := range strings.Split(dlg, "<form")[1:] {
		f = f[:strings.Index(f, ">")]
		if strings.Contains(f, "/remove") {
			if !strings.Contains(f, "data-confirm") {
				t.Error("Remove does not confirm")
			}
			continue
		}
		if !strings.Contains(f, "data-save") {
			t.Errorf("a form of the dialog does not save in place: <form%s>", f)
		}
	}
	helper, aliases := tpl("helper-model"), tpl("aliases-grok")
	if !strings.Contains(helper, `data-section="model"`) || !strings.Contains(helper, "data-autosave") || !strings.Contains(helper, `action="/admin/providers/grok/models/reload"`) || !strings.Contains(helper, "data-quiet") {
		t.Error("the helper dialog saves on change and reloads the model list when it opens")
	}
	if strings.Contains(helper, "Reload models") || strings.Contains(helper, `>Save</button>`) || strings.Contains(helper, "unsaved") {
		t.Error("the helper dialog still has Reload, Save or the unsaved mark")
	}
	for _, w := range []string{`data-section="aliases"`, ">Add alias</button>", `action="/admin/providers/grok/aliases/put"`, "data-autosave"} {
		if !strings.Contains(aliases, w) {
			t.Errorf("the alias dialog lacks %q", w)
		}
	}
	if strings.Contains(aliases, `>Save</button>`) {
		t.Error("an alias row still has its own Save")
	}
}

// Sections of the dialog in jsdom: edits are tracked per section, closing with unsaved edits asks first (Close and
// Escape), and saving one section updates the page without losing what is typed in another. Needs node and jsdom.
func TestProviderDialogDirtyStateInJSDOM(t *testing.T) {
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
	if err := probe.Run(); err != nil {
		t.Skip("jsdom is not available (set SKGATE_JSDOM to a directory with node_modules/jsdom)")
	}
	up, _ := aliasUpstream(t)
	_, _, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	dir := t.TempDir()
	save := func(name string) {
		_, page := br.get("/admin")
		if err := os.WriteFile(filepath.Join(dir, name), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	save("before.html")
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"grok-mini"}})
	save("after.html")
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "dirty.js"))
	cmd := exec.Command(node, script, dir, js)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// In a real browser (optional, like the layout sweep): adding an alias updates the card and the alias table
// without a reload or a scroll jump, the helper model saves when a field changes, and the alias table opens
// and deletes an alias.
func TestProviderDialogSectionsInBrowser(t *testing.T) {
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the dialog check in a browser")
	}
	up, _ := aliasUpstream(t)
	_, ts, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	script, _ := filepath.Abs(filepath.Join("testdata", "providerdialog.js"))
	out, err := exec.Command(node, script, ts.URL, chrome, filepath.Join(pp, "node_modules", "puppeteer-core"), browserCookies(br, ts.URL)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}
