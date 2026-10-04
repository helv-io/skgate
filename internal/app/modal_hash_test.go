package app

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/provider/grok"
)

// Every dialog opened from a page is addressed by its URL fragment: the Details of a provider, key, client and
// upstream, and the helper model picker. The markup gives each one a template id and an opener that agree.
func TestEveryDialogIsHashAddressable(t *testing.T) {
	a, br, csrf := managedApp(t)
	a.Providers.Default().(*grok.Client).SetTokens("acc", "ref", time.Now().Add(time.Hour))
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", url.Values{"lifecycle": {"always"}}))
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"c"}, "redirects": {"https://x.example/cb"}, "method": {"client_secret_post"}})
	opener := regexp.MustCompile(`data-dialog-open="#([^"]+)"`)
	for path, prefix := range map[string]string{"/admin": "provider-grok", "/admin/keys": "key-", "/admin/clients": "client-skc-", "/admin/upstreams": "upstream-m"} {
		_, page := br.get(path)
		found := false
		for _, m := range opener.FindAllStringSubmatch(page, -1) {
			if strings.HasPrefix(m[1], prefix) {
				found = true
				if !regexp.MustCompile(`<template data-dialog-content id="` + regexp.QuoteMeta(m[1]) + `"`).MatchString(page) {
					t.Errorf("%s: opener #%s has no dialog template", path, m[1])
				}
			}
		}
		if !found {
			t.Errorf("%s has no opener with an id starting %q", path, prefix)
		}
	}
	_, page := br.get("/admin/upstreams/new")
	if !strings.Contains(page, `data-dialog-open="#helper-model"`) || !strings.Contains(page, `<template data-dialog-content id="helper-model"`) {
		t.Error("the helper model picker must be a fragment-addressed dialog")
	}
}

// The shared modal keeps the address in step with what is shown (jsdom): opening sets the fragment, closing by
// the button, Escape or the backdrop clears it, Back closes and Forward reopens, and a load or hashchange with
// the fragment opens the dialog. Needs node and jsdom (SKGATE_JSDOM=<dir containing node_modules>).
func TestModalFragmentInJSDOM(t *testing.T) {
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
	a, br, csrf := managedApp(t)
	a.Providers.Default().(*grok.Client).SetTokens("acc", "ref", time.Now().Add(time.Hour))
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", url.Values{"lifecycle": {"always"}}))
	br.post("/admin/upstreams/save", stdioForm(csrf, "n", url.Values{"lifecycle": {"always"}}))
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"c"}, "redirects": {"https://x.example/cb"}, "method": {"client_secret_post"}})
	dir := t.TempDir()
	type kase struct {
		Page    string `json:"page"`
		Path    string `json:"path"`
		ID      string `json:"id"`
		Second  string `json:"second,omitempty"`
		Confirm bool   `json:"confirm,omitempty"`
		Info    bool   `json:"info,omitempty"`
	}
	var cases []kase
	idOf := func(page, prefix string) string {
		return regexp.MustCompile(`data-dialog-open="#(` + regexp.QuoteMeta(prefix) + `[^"]*)"`).FindStringSubmatch(page)[1]
	}
	for _, c := range []struct{ file, path, prefix string }{
		{"status.html", "/admin", "provider-grok"}, {"keys.html", "/admin/keys", "key-"},
		{"clients.html", "/admin/clients", "client-"}, {"list.html", "/admin/upstreams", "upstream-m"},
		{"new.html", "/admin/upstreams/new", "helper-model"},
	} {
		_, page := br.get(c.path)
		if err := os.WriteFile(filepath.Join(dir, c.file), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
		k := kase{Page: c.file, Path: c.path, ID: idOf(page, c.prefix), Info: c.prefix == "client-"}
		if c.prefix == "upstream-m" {
			k.Second, k.Confirm = "upstream-n", true
		}
		cases = append(cases, k)
	}
	arg, _ := json.Marshal(cases)
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "modal_hash.js"))
	cmd := exec.Command(node, script, dir, js, string(arg))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The expiration field behaves in jsdom: debounced live preview from the server's parser, presets fill the text,
// the newest answer wins and a failed check does not block the form.
func TestExpiryFieldInJSDOM(t *testing.T) {
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
	_, _, br, _ := signedIn(t, nil)
	_, page := br.get("/admin/keys")
	file := filepath.Join(t.TempDir(), "keys.html")
	if err := os.WriteFile(file, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "expiry.js"))
	cmd := exec.Command(node, script, file, js)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Only a dialog that shows information alone may close by a click on the backdrop. It says so with the explicit
// data-informational flag, and the flag is refused on a dialog with a form, field or button in it; every dialog
// that has one is therefore left open by a stray click.
func TestOnlyInformationalDialogsCloseByTheBackdrop(t *testing.T) {
	a, br, csrf := managedApp(t)
	a.Providers.Default().(*grok.Client).SetTokens("acc", "ref", time.Now().Add(time.Hour))
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", nil))
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"c"}, "redirects": {"https://x.example/cb"}, "method": {"client_secret_post"}})
	tpl := regexp.MustCompile(`(?s)<template data-dialog-content id="([^"]+)"[^>]*?>(.*?)</template>`)
	interactive := regexp.MustCompile(`<(form|input|select|textarea)\b|<button\b[^>]*>|<a [^>]*class="act`)
	// a copybox is a button, but it changes nothing: a dialog holding only copyboxes is still informational
	copybox := regexp.MustCompile(`<button type="button" class="copybox[^>]*>.*?</button>`)
	interactiveIn := func(h string) bool { return interactive.MatchString(copybox.ReplaceAllString(h, "")) }
	informational, withForms := 0, 0
	for _, path := range []string{"/admin", "/admin/keys", "/admin/clients", "/admin/upstreams"} {
		_, page := br.get(path)
		for _, m := range tpl.FindAllStringSubmatch(page, -1) {
			flagged := regexp.MustCompile(`<template data-dialog-content id="` + regexp.QuoteMeta(m[1]) + `"[^>]*data-informational`).MatchString(page)
			switch {
			case flagged && interactiveIn(m[2]):
				t.Errorf("%s: dialog %s is flagged informational but has a form, field or button", path, m[1])
			case flagged:
				informational++
			case interactiveIn(m[2]):
				withForms++
			default:
				t.Errorf("%s: dialog %s shows information only; flag it with infodlg", path, m[1])
			}
		}
	}
	if informational == 0 || withForms == 0 {
		t.Errorf("expected both kinds of dialog: %d informational, %d with forms", informational, withForms)
	}
	js, _ := os.ReadFile(filepath.Join("..", "admin", "static", "app.js"))
	if !strings.Contains(string(js), "e.target === dlg && informational") {
		t.Error("the shared modal must close on the backdrop only when the dialog is informational")
	}
}
