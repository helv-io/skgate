package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func jsdomEnv(t *testing.T) (string, []string) {
	t.Helper()
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
		t.Skip("jsdom is not available")
	}
	return node, env
}

func TestLeaveGuardInJSDOM(t *testing.T) {
	node, env := jsdomEnv(t)
	_, br, _ := managedApp(t)
	dir := t.TempDir()
	for file, path := range map[string]string{"new.html": "/admin/upstreams/new", "status.html": "/admin"} {
		_, page := br.get(path)
		if err := os.WriteFile(filepath.Join(dir, file), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "leave.js"))
	cmd := exec.Command(node, script, dir, js)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUpdatePillInJSDOM(t *testing.T) {
	node, env := jsdomEnv(t)
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "update_pill.js"))
	cmd := exec.Command(node, script, js)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}
