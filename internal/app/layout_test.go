package app

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Forms have one right edge: controls have no fixed widths of their own (they fill their container),
// and every form that holds fields is a .form, the one shared column.
func TestFormsShareOneColumn(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("..", "admin", "static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(string(css), -1) {
		sel, body := strings.TrimSpace(rule[1]), rule[2]
		if !regexp.MustCompile(`(^|[ ,>])(input|select|textarea)\b`).MatchString(sel) || strings.Contains(sel, "checkbox") {
			continue
		}
		if regexp.MustCompile(`(^|;)\s*(max-)?width:\s*\d+(px|em|rem|ch)`).MatchString(body) {
			t.Errorf("control rule %q sets a fixed width: %s", sel, body)
		}
	}
	files, _ := filepath.Glob(filepath.Join("..", "admin", "templates", "*.html"))
	formRE := regexp.MustCompile(`<form\b[^>]*>`)
	for _, f := range files {
		b, _ := os.ReadFile(f)
		s := string(b)
		for _, loc := range formRE.FindAllStringIndex(s, -1) {
			tag := s[loc[0]:loc[1]]
			end := strings.Index(s[loc[1]:], "</form>")
			if end < 0 {
				continue
			}
			body := s[loc[1] : loc[1]+end]
			holdsFields := strings.Contains(body, `class="field"`) || strings.Contains(body, `{{template "upstream_fields"`) ||
				regexp.MustCompile(`<(input|select|textarea)\b[^>]*type="(text|password|url)"`).MatchString(body) || strings.Contains(body, "<select") || strings.Contains(body, "<textarea")
			if holdsFields && !regexp.MustCompile(`class="[^"]*\bform\b`).MatchString(tag) {
				t.Errorf("%s: a form with fields must have class \"form\": %s", filepath.Base(f), tag)
			}
		}
	}
}

// In a real browser (optional): every input, select, textarea, pairs row and button row of the upstream,
// client and import forms ends at the same right edge as the first top-level field, at three widths.
// Needs Chrome and puppeteer-core: SKGATE_CHROME=/usr/bin/google-chrome SKGATE_PUPPETEER=<dir with node_modules/puppeteer-core>.
func TestFormRightEdgesInBrowser(t *testing.T) {
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the browser layout check")
	}
	a, br, csrf := managedApp(t)
	_ = a
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", url.Values{"lifecycle": {"always"}}))
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"r"}, "url": {"http://127.0.0.1:1/mcp"},
		"auth_kind": {"header"}, "auth_name": {"X-Api-Key"}, "auth_value": {"k"}, "hdr_name": {"X-A"}, "hdr_value": {"1"}, "host_override": {"svc:8000"}, "enabled": {"1"}})
	dir := t.TempDir()
	css, _ := os.ReadFile(filepath.Join("..", "admin", "static", "app.css"))
	js, _ := os.ReadFile(filepath.Join("..", "admin", "static", "app.js"))
	os.WriteFile(filepath.Join(dir, "app.css"), css, 0o600)
	os.WriteFile(filepath.Join(dir, "app.js"), js, 0o600)
	for file, path := range map[string]string{"upstreams": "/admin/upstreams", "edit_managed": "/admin/upstreams/edit?alias=m", "edit_remote": "/admin/upstreams/edit?alias=r",
		"clients": "/admin/clients", "import": "/admin/upstreams/import"} {
		_, page := br.get(path)
		os.WriteFile(filepath.Join(dir, file+".html"), []byte(strings.ReplaceAll(page, "/admin/static/", "")), 0o600)
	}
	script, _ := filepath.Abs(filepath.Join("testdata", "edges.js"))
	out, err := exec.Command(node, script, dir, chrome, filepath.Join(pp, "node_modules", "puppeteer-core")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// In a real browser (optional, same switches as above): the action buttons of the upstreams, keys and
// clients tables line up as columns, with one width per position, whatever the label (detect, process).
func TestTableButtonsAlignInBrowser(t *testing.T) {
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the browser layout check")
	}
	a, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", url.Values{"lifecycle": {"always"}}))
	br.post("/admin/upstreams/save", stdioForm(csrf, "n", nil))
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"docs"}, "url": {"http://127.0.0.1:1/mcp"},
		"auth_kind": {"auto"}, "host_override": {"ha:8123"}, "enabled": {"1"}})
	_ = a
	for _, l := range []string{"one", "two"} {
		br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {l}})
	}
	for _, n := range []string{"c1", "c2"} {
		br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {n}, "redirects": {"https://x.example/cb"}, "method": {"none"}})
	}
	dir := t.TempDir()
	css, _ := os.ReadFile(filepath.Join("..", "admin", "static", "app.css"))
	os.WriteFile(filepath.Join(dir, "app.css"), css, 0o600)
	for file, path := range map[string]string{"upstreams": "/admin/upstreams", "keys": "/admin/keys", "clients": "/admin/clients"} {
		_, page := br.get(path)
		os.WriteFile(filepath.Join(dir, file+".html"), []byte(strings.ReplaceAll(page, "/admin/static/", "")), 0o600)
	}
	script, _ := filepath.Abs(filepath.Join("testdata", "buttons.js"))
	out, err := exec.Command(node, script, dir, chrome, filepath.Join(pp, "node_modules", "puppeteer-core")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
