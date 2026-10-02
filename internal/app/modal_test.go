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

// The admin UI never uses the browser's alert, confirm or prompt: confirmations go through the one
// shared modal, wired by data-confirm.
func TestNoBrowserDialogsAndOneSharedModal(t *testing.T) {
	root := filepath.Join("..", "admin")
	bad := regexp.MustCompile(`\b(window\.)?(alert|confirm|prompt)\s*\(|onsubmit\s*=|onclick\s*=`)
	for _, dir := range []string{"templates", "static"} {
		files, _ := filepath.Glob(filepath.Join(root, dir, "*"))
		for _, f := range files {
			b, _ := os.ReadFile(f)
			if m := bad.FindString(string(b)); m != "" {
				t.Errorf("%s uses %q", f, m)
			}
		}
	}
	a, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", nil))
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	_ = a
	confirm := regexp.MustCompile(`<form[^>]*data-confirm="([^"]*)"`)
	for _, path := range []string{"/admin", "/admin/keys", "/admin/clients", "/admin/upstreams", "/admin/upstreams/logs?alias=m"} {
		_, page := br.get(path)
		if strings.Count(page, "<dialog") != 1 || !strings.Contains(page, "data-modal-ok") {
			t.Errorf("%s must carry exactly one shared modal", path)
		}
		for _, m := range confirm.FindAllStringSubmatch(page, -1) {
			if len(m[1]) == 0 || len(m[1]) > 120 {
				t.Errorf("%s: confirm text %q must be short and non-empty", path, m[1])
			}
		}
	}
	// destructive actions ask first
	_, keys := br.get("/admin/keys")
	_, list := br.get("/admin/upstreams")
	_, logs := br.get("/admin/upstreams/logs?alias=m")
	for name, want := range map[string]struct{ page, action string }{
		"revoke key": {keys, "/admin/keys/revoke"}, "regenerate key": {keys, "/admin/keys/regenerate"},
		"delete upstream": {list, "/admin/upstreams/delete"},
	} {
		if !regexp.MustCompile(`<form method="post" action="` + regexp.QuoteMeta(want.action) + `"[^>]*data-confirm=`).MatchString(want.page) {
			t.Errorf("%s has no confirmation", name)
		}
	}
	if !strings.Contains(logs, `data-confirm="Stop the process?`) {
		t.Error("stop has no confirmation")
	}
}

// Behavior of the modal in jsdom: opens on submit, Escape/backdrop/Cancel close without submitting,
// Confirm submits once, destructive actions default-focus Cancel, focus returns to the opener.
// Needs node and jsdom (NODE_PATH or SKGATE_JSDOM=<dir containing node_modules>); skipped otherwise.
func TestModalBehaviorInJSDOM(t *testing.T) {
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
	_, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", url.Values{"lifecycle": {"always"}}))
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	dir := t.TempDir()
	for file, path := range map[string]string{"keys.html": "/admin/keys", "logs.html": "/admin/upstreams/logs?alias=m", "list.html": "/admin/upstreams", "clients.html": "/admin/clients", "status.html": "/admin"} {
		_, page := br.get(path)
		if err := os.WriteFile(filepath.Join(dir, file), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "modal.js"))
	cmd := exec.Command(node, script, dir, js)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%s", out)
	}
}
