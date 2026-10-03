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
	_, page := br.get("/admin/upstreams")
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
	}
	var cases []kase
	idOf := func(page, prefix string) string {
		return regexp.MustCompile(`data-dialog-open="#(` + regexp.QuoteMeta(prefix) + `[^"]*)"`).FindStringSubmatch(page)[1]
	}
	for _, c := range []struct{ file, path, prefix string }{
		{"status.html", "/admin", "provider-grok"}, {"keys.html", "/admin/keys", "key-"},
		{"clients.html", "/admin/clients", "client-"}, {"list.html", "/admin/upstreams", "upstream-m"},
		{"list.html", "/admin/upstreams", "helper-model"},
	} {
		_, page := br.get(c.path)
		if err := os.WriteFile(filepath.Join(dir, c.file), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
		k := kase{Page: c.file, Path: c.path, ID: idOf(page, c.prefix)}
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
