package app

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/mcp"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitOrigin makes a bare repository with one commit that is a valid fake MCP server launcher input.
func gitOrigin(t *testing.T) (bare, wc string) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	d := t.TempDir()
	bare, wc = filepath.Join(d, "o.git"), filepath.Join(d, "wc")
	gitRun(t, d, "init", "-q", "--bare", "-b", "main", bare)
	gitRun(t, d, "clone", "-q", bare, wc)
	gitRun(t, wc, "checkout", "-q", "-B", "main")
	push := func(v string) {
		os.WriteFile(filepath.Join(wc, "v"), []byte(v), 0o644)
		gitRun(t, wc, "add", "v")
		gitRun(t, wc, "commit", "-q", "-m", v)
		gitRun(t, wc, "push", "-q", "origin", "main")
	}
	push("1")
	return bare, wc
}

func TestAdminUpdateTrackingAutoUpdateAndCheck(t *testing.T) {
	a, br, csrf := managedApp(t)
	bare, wc := gitOrigin(t)
	form := stdioForm(csrf, "repo", url.Values{"kind": {"git"}, "git_url": {"file://" + bare}, "git_ref": {"main"}, "auto_update": {"3600"}, "lifecycle": {"always"}})
	r, _ := br.post("/admin/upstreams/save", form)
	if r.StatusCode != 303 || flashKind(r) != "ok" {
		k, m := flashOf(r)
		t.Fatalf("create: %d %s %s", r.StatusCode, k, m)
	}
	u, _ := a.MCP.Upstreams.Get("repo")
	if u.AutoUpdateSecs != 3600 {
		t.Fatalf("interval not stored: %+v", u)
	}
	end := time.Now().Add(15 * time.Second)
	for a.MCP.Managed.Lookup("repo").Rev() == "" && time.Now().Before(end) {
		time.Sleep(50 * time.Millisecond)
	}
	rev := a.MCP.Managed.Lookup("repo").Rev()
	if len(rev) != 12 {
		t.Fatalf("installed rev %q", rev)
	}
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, "\ninstalled: "+rev+" main") || strings.Contains(list, "update available") || strings.Contains(list, ">update</span>") {
		t.Fatalf("list must show the installed commit and ref:\n%s", list)
	}
	_, page := br.get("/admin/upstreams/logs?alias=repo")
	if !strings.Contains(page, "<code>"+rev+"</code>") || !strings.Contains(page, "hourly") || strings.Contains(page, `class="act accent"`) {
		t.Fatal("process page: installed commit and interval, update not emphasized")
	}
	// the remote moves; a check flags it, the button is emphasized and stays usable
	os.WriteFile(filepath.Join(wc, "v"), []byte("2"), 0o644)
	gitRun(t, wc, "commit", "-q", "-am", "2")
	gitRun(t, wc, "push", "-q", "origin", "main")
	res, _ := br.post("/admin/upstreams/process", url.Values{"csrf": {csrf}, "alias": {"repo"}, "action": {"check"}, "to": {"logs"}})
	if _, m := flashOf(res); !strings.Contains(m, "update available") {
		t.Fatalf("check: %q", m)
	}
	_, list = br.get("/admin/upstreams")
	_, page = br.get("/admin/upstreams/logs?alias=repo")
	if !strings.Contains(list, "\nupdate available") || !strings.Contains(list, `class="pill warn" title="the remote ref is ahead of the installed commit">update</span>`) || !regexp.MustCompile(`class="act accent">update</button>`).MatchString(page) {
		t.Fatal("update available must show on the list and emphasize the button")
	}
	if r, _ := br.get("/admin/upstreams/process?alias=repo&action=check"); r.StatusCode != 405 {
		t.Fatalf("GET check: %d", r.StatusCode)
	}
	// update applies it
	br.post("/admin/upstreams/process", url.Values{"csrf": {csrf}, "alias": {"repo"}, "action": {"update"}, "to": {"logs"}})
	end = time.Now().Add(20 * time.Second)
	for a.MCP.Managed.Lookup("repo").Rev() == rev && time.Now().Before(end) {
		time.Sleep(50 * time.Millisecond)
	}
	if a.MCP.Managed.Lookup("repo").Rev() == rev {
		t.Fatal("update did not apply")
	}
	// edit form keeps the interval; a value below the minimum is refused
	_, edit := br.get("/admin/upstreams/edit?alias=repo")
	if !regexp.MustCompile(`<option value="3600" selected>`).MatchString(edit) {
		t.Fatal("edit form must preselect the interval")
	}
	bad := stdioForm(csrf, "repo", url.Values{"mode": {"edit"}, "kind": {"git"}, "git_url": {"file://" + bare}, "auto_update": {"60"}})
	if r, _ := br.post("/admin/upstreams/save", bad); flashKind(r) != "bad" {
		t.Fatal("an interval under 5 minutes must be refused")
	}
	// export/import round trip keeps the interval
	ups, _ := a.MCP.Upstreams.List()
	if !strings.Contains(string(mcp.ExportJSON(ups)), `"autoUpdateSeconds": 3600`) {
		t.Fatalf("export: %s", mcp.ExportJSON(ups))
	}
}

// The Home Assistant preset is plain markup (no per-page script): its option carries the values the
// shared handler copies into the form, and the client it produces works with the token endpoint.
func TestClientPresetMarkupAndHomeAssistantFlow(t *testing.T) {
	_, br, csrf := managedApp(t)
	_, page := br.get("/admin/clients")
	for _, want := range []string{`<select data-preset>`, `<option value="">Custom</option>`, `data-set-redirects="https://my.home-assistant.io/redirect/oauth"`, `data-set-method="client_secret_post"`} {
		if !strings.Contains(page, want) {
			t.Errorf("clients page lacks %s", want)
		}
	}
	r, body := br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"Home Assistant"}, "redirects": {"https://my.home-assistant.io/redirect/oauth"}, "method": {"client_secret_post"}})
	if r.StatusCode != 200 || !strings.Contains(body, "new-client-secret") {
		t.Fatalf("create: %d", r.StatusCode)
	}
}

func TestCommandFieldIsADropdownOfInstalledCommandsWithCustom(t *testing.T) {
	a, br, csrf := managedApp(t)
	_, page := br.get("/admin/upstreams")
	sel := regexp.MustCompile(`(?s)<select name="command_pick" data-pick>(.*?)</select>`).FindStringSubmatch(page)
	if sel == nil {
		t.Fatal("no command dropdown")
	}
	opts := regexp.MustCompile(`<option value="([^"]*)"`).FindAllStringSubmatch(sel[1], -1)
	if len(opts) < 2 || opts[len(opts)-1][1] != "" || !strings.Contains(sel[1], "Custom path…") {
		t.Fatalf("options: %v", opts)
	}
	for _, o := range opts[:len(opts)-1] {
		if _, err := exec.LookPath(o[1]); err != nil {
			t.Errorf("%s is listed but not installed", o[1])
		}
	}
	if strings.Contains(sel[1], `value="docker"`) {
		if _, err := exec.LookPath("docker"); err != nil {
			t.Error("docker listed without being installed")
		}
	}
	if !strings.Contains(page, `data-pick-custom hidden`) {
		t.Error("a new upstream starts on a listed command with the path input hidden")
	}
	// a listed choice wins over the hidden text; Custom uses the typed path
	form := stdioForm(csrf, "listed", url.Values{"command": {"stale"}, "command_pick": {"sh"}, "args": {"-c", "true"}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal("save with a listed command")
	}
	if u, _ := a.MCP.Upstreams.Get("listed"); u.Command != "sh" {
		t.Fatalf("command %q", u.Command)
	}
	form = stdioForm(csrf, "custom", url.Values{"command": {"/opt/tool/bin/run"}, "command_pick": {""}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal("save with a custom path")
	}
	// existing upstreams: listed loads selected, unlisted loads as Custom with the value kept
	_, e1 := br.get("/admin/upstreams/edit?alias=listed")
	if !regexp.MustCompile(`<option value="sh" selected>`).MatchString(e1) || !strings.Contains(e1, `data-pick-custom hidden`) {
		t.Error("a listed command loads selected")
	}
	_, e2 := br.get("/admin/upstreams/edit?alias=custom")
	if !regexp.MustCompile(`<option value="" selected>Custom path…`).MatchString(e2) || !strings.Contains(e2, `value="/opt/tool/bin/run"`) || strings.Contains(e2, `data-pick-custom hidden`) {
		t.Error("an unlisted command loads as Custom with its value")
	}
}
