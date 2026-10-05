package app

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/oidctest"
)

func managedApp(t *testing.T) (*App, *browser, string) {
	a, _, br, csrf := signedIn(t, func(c *config.Config, _ *oidctest.Provider) {
		c.ManagedDir, c.ManagedMaxProcs = t.TempDir(), 4
		c.ManagedStopGrace, c.ManagedLogLines, c.ManagedInstallMax = 2*time.Second, 200, 20*time.Second
	})
	t.Cleanup(a.MCP.ShutdownManaged)
	return a, br, csrf
}

func stdioForm(csrf, alias string, extra url.Values) url.Values {
	v := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"stdio"}, "alias": {alias}, "command": {os.Args[0]},
		"env_name": {"FAKE_MCP", "API_TOKEN", ""}, "env_value": {"1", "super-secret-token-4321", ""},
		"lifecycle": {"on-demand"}, "enabled": {"1"}, "include": {"1"}, "startup_secs": {"20"}}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

func TestAdminManagedCreateEditMaskAndKeepSecret(t *testing.T) {
	a, br, csrf := managedApp(t)
	r, _ := br.post("/admin/upstreams/save", stdioForm(csrf, "tools", url.Values{"args": {"-x", "", "--flag=a b", ""}}))
	if r.StatusCode != 303 || flashKind(r) != "ok" {
		k, m := flashOf(r)
		t.Fatalf("create: %d %s %s", r.StatusCode, k, m)
	}
	u, _ := a.MCP.Upstreams.Get("tools")
	if u.Kind != mcp.KindStdio || u.Command != os.Args[0] || len(u.Args) != 2 || u.Args[1] != "--flag=a b" || len(u.Env) != 2 || u.Env[1].Value != "super-secret-token-4321" || u.StartupSecs != 20 {
		t.Fatalf("%+v", u)
	}
	_, list := br.get("/admin/upstreams")
	_, edit := br.get("/admin/upstreams/tools/edit")
	for name, page := range map[string]string{"list": list, "edit": edit} {
		if strings.Contains(page, "super-secret-token-4321") {
			t.Errorf("%s page shows a secret env value", name)
		}
	}
	if !strings.Contains(edit, "************4321") {
		t.Fatalf("edit page must show the masked value (12 asterisks + last 4)")
	}
	// saving with the masked value (or empty) keeps the stored secret; a new value replaces it
	form := stdioForm(csrf, "tools", url.Values{"mode": {"edit"}, "env_value": {"1", "************4321", ""}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	if u, _ = a.MCP.Upstreams.Get("tools"); u.Env[1].Value != "super-secret-token-4321" {
		t.Fatalf("masked value must keep the secret: %+v", u.Env)
	}
	form = stdioForm(csrf, "tools", url.Values{"mode": {"edit"}, "env_value": {"1", "", ""}})
	br.post("/admin/upstreams/save", form)
	if u, _ = a.MCP.Upstreams.Get("tools"); u.Env[1].Value != "super-secret-token-4321" {
		t.Fatalf("empty value must keep the secret: %+v", u.Env)
	}
	form = stdioForm(csrf, "tools", url.Values{"mode": {"edit"}, "env_value": {"1", "rotated-secret-9999", ""}})
	br.post("/admin/upstreams/save", form)
	if u, _ = a.MCP.Upstreams.Get("tools"); u.Env[1].Value != "rotated-secret-9999" {
		t.Fatalf("%+v", u.Env)
	}
	// removing the row removes the variable
	form = stdioForm(csrf, "tools", url.Values{"mode": {"edit"}, "env_name": {"FAKE_MCP", "", ""}, "env_value": {"1", "", ""}})
	br.post("/admin/upstreams/save", form)
	if u, _ = a.MCP.Upstreams.Get("tools"); len(u.Env) != 1 {
		t.Fatalf("%+v", u.Env)
	}
	// the type cannot change on edit
	form = stdioForm(csrf, "tools", url.Values{"mode": {"edit"}, "kind": {"remote"}, "url": {"http://x/mcp"}, "auth_kind": {"none"}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "bad" {
		t.Fatal("changing the type must be refused")
	}
}

func TestAdminManagedHiddenSettingsKept(t *testing.T) {
	a, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "w", nil))
	u, _ := a.MCP.Upstreams.Get("w")
	custom := t.TempDir()
	u.WorkDir = custom
	if err := a.MCP.Upstreams.Update(u, true); err != nil {
		t.Fatal(err)
	}
	_, edit := br.get("/admin/upstreams/w/edit")
	if strings.Contains(strings.ToLower(edit), "working dir") || strings.Contains(edit, `name="workdir"`) || strings.Contains(edit, custom) {
		t.Fatal("the edit form must not show a working dir")
	}
	u.IdleSecs, u.StartupSecs = 123, 77
	if err := a.MCP.Upstreams.Update(u, true); err != nil {
		t.Fatal(err)
	}
	_, edit = br.get("/admin/upstreams/w/edit")
	if strings.Contains(edit, `name="idle_secs"`) || strings.Contains(strings.ToLower(edit), "startup timeout") || strings.Contains(edit, `type="number" name="startup_secs"`) {
		t.Fatal("the edit form must not show an idle or startup timeout")
	}
	if !strings.Contains(edit, `type="hidden" name="startup_secs" value="77"`) {
		t.Fatal("the stored startup timeout must ride along as a hidden value")
	}
	// a save (even one that posts a stray workdir) keeps the stored value
	form := stdioForm(csrf, "w", url.Values{"mode": {"edit"}, "workdir": {"/elsewhere"}, "startup_secs": {"77"}})
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	if u, _ = a.MCP.Upstreams.Get("w"); u.WorkDir != custom || u.IdleSecs != 123 || u.StartupSecs != 77 {
		t.Fatalf("stored hidden settings lost: %q %d %d", u.WorkDir, u.IdleSecs, u.StartupSecs)
	}
	// new upstreams never get one
	br.post("/admin/upstreams/save", stdioForm(csrf, "n", url.Values{"workdir": {"/x"}}))
	if u, _ = a.MCP.Upstreams.Get("n"); u.WorkDir != "" {
		t.Fatalf("new upstream got %q", u.WorkDir)
	}
}

func TestAdminManagedValidationErrors(t *testing.T) {
	_, br, csrf := managedApp(t)
	for name, v := range map[string]url.Values{
		"empty command": stdioForm(csrf, "a", url.Values{"command": {"  "}}),
		"bad env name":  stdioForm(csrf, "b", url.Values{"env_name": {"1BAD"}, "env_value": {"x"}}),
		"bad timeout":   stdioForm(csrf, "c", url.Values{"startup_secs": {"soon"}}),
		"bad alias":     stdioForm(csrf, "Bad_Alias", nil),
		"git no url":    stdioForm(csrf, "e", url.Values{"kind": {"git"}}),
		"git ssh":       stdioForm(csrf, "f", url.Values{"kind": {"git"}, "git_url": {"ssh://git@h/x.git"}}),
		"unknown kind":  stdioForm(csrf, "g", url.Values{"kind": {"docker"}}),
	} {
		r, _ := br.post("/admin/upstreams/save", v)
		if r.StatusCode != 303 || flashKind(r) != "bad" {
			t.Errorf("%s: %d %s", name, r.StatusCode, flashKind(r))
		}
	}
}

func TestAdminSlimShowsNoticeAndRefuses(t *testing.T) {
	old := config.Edition
	config.Edition = "slim"
	t.Cleanup(func() { config.Edition = old })
	a, _, br, csrf := signedIn(t, nil)
	_, page := br.get("/admin/upstreams/new")
	if !strings.Contains(page, "slim image") || !regexp.MustCompile(`<option value="stdio"[^>]*disabled`).MatchString(page) {
		t.Fatal("the add form must show the notice and disable the managed types")
	}
	r, _ := br.post("/admin/upstreams/save", stdioForm(csrf, "tools", nil))
	if r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Fatal("managed save must be refused")
	}
	if _, ok := a.MCP.Upstreams.Get("tools"); ok {
		t.Fatal("nothing may be stored")
	}
	_, imp := br.get("/admin/upstreams/import")
	if !strings.Contains(imp, "slim image") {
		t.Fatal("import page must carry the notice")
	}
}

func TestAdminManagedProcessActionsAndLogsPage(t *testing.T) {
	a, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "tools", url.Values{"lifecycle": {"always"}}))
	act := func(action string) *string {
		r, _ := br.post("/admin/upstreams/tools/process", url.Values{"csrf": {csrf}, "action": {action}, "to": {"logs"}})
		_, m := flashOf(r)
		return &m
	}
	waitState := func(want string) {
		t.Helper()
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) {
			if p := a.MCP.Managed.Lookup("tools"); p != nil && p.Status().State == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("state never became %s", want)
	}
	waitState("running") // always-on: started by the save
	_, page := br.get("/admin/upstreams/tools/logs")
	if !pillRE("ok", "running").MatchString(page) {
		t.Fatalf("logs page must show the running pill")
	}
	if !strings.Contains(page, "data-log-view") || !strings.Contains(page, `class="logbar"`) {
		t.Fatal("logs page must use the shared output viewer")
	}
	pid := a.MCP.Managed.Lookup("tools").Status().PID
	if m := act("stop"); !strings.Contains(*m, "stopped") {
		t.Fatal(*m)
	}
	waitState("stopped")
	_, list := br.get("/admin/upstreams")
	if !regexp.MustCompile(`<a class="pill off" href="/admin/upstreams/tools/logs" title="[^"]*">stopped</a>`).MatchString(list) {
		t.Fatal("list must show the stopped pill as a link to the process page")
	}
	if strings.Contains(list, ">Process</a>") {
		t.Fatal("the list must not have a separate Process button")
	}
	// the pill is a GET of the process page: it must not start a stopped process
	br.get("/admin/upstreams/tools/logs")
	if st := a.MCP.Managed.Lookup("tools").Status(); st.State != "stopped" {
		t.Fatalf("opening the process page started the process: %s", st.State)
	}
	if m := act("start"); !strings.Contains(*m, "start requested") {
		t.Fatal(*m)
	}
	waitState("running")
	if a.MCP.Managed.Lookup("tools").Status().PID == pid {
		t.Fatal("start after stop must spawn a new process")
	}
	before := a.MCP.Managed.Lookup("tools").Status().PID
	act("restart")
	waitState("running")
	for end := time.Now().Add(10 * time.Second); a.MCP.Managed.Lookup("tools").Status().PID == before; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("restart left the old process running")
		}
	}
	if m := act("update"); !strings.Contains(*m, "update started") {
		t.Fatalf("update on a command upstream: %s", *m)
	}
	end := time.Now().Add(10 * time.Second)
	for a.MCP.Managed.Lookup("tools").UpdateInfo().LastUpdate == "" { // the update runs in the background
		if time.Now().After(end) {
			t.Fatal("the update never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitState("running")
	if m := act("check"); !strings.Contains(*m, "only git upstreams") {
		t.Fatalf("check on a command upstream: %s", *m)
	}
	if m := act("bogus"); !strings.Contains(*m, "unknown action") {
		t.Fatal(*m)
	}
	// the log ring got skgate's own notes; clearing empties it
	if len(a.MCP.ProcessLogs("tools", 0)) == 0 {
		t.Fatal("the ring has no lines to clear: the check below would prove nothing")
	}
	act("clear-logs")
	if len(a.MCP.ProcessLogs("tools", 0)) != 0 {
		t.Fatal("logs not cleared")
	}
	// GET must never trigger an action
	r, _ := br.get("/admin/upstreams/tools/process?action=stop")
	if r.StatusCode != 405 {
		t.Fatalf("GET action: %d", r.StatusCode)
	}
	// a remote upstream has no process page
	up := fakeUpstream(t)
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "rem", URL: up.URL, AuthKind: mcp.AuthNone, Enabled: true})
	if r, _ := br.get("/admin/upstreams/rem/logs"); r.StatusCode != 303 {
		t.Fatalf("remote logs page: %d", r.StatusCode)
	}
}

func TestAdminManagedTestButtonAndStderrOnLogsPage(t *testing.T) {
	a, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "tools", nil))
	_, page := br.get("/admin/upstreams/tools/test")
	if !strings.Contains(page, `class="pill ok">OK`) || !strings.Contains(page, "<code>echo</code>") || !strings.Contains(page, "fake-stdio") {
		t.Fatalf("test page: %.400s", page)
	}
	if strings.Contains(page, "Trailing slash") || strings.Contains(page, "HTTP status") {
		t.Fatal("HTTP-only rows must not appear for managed upstreams")
	}
	for _, gone := range []string{">Back</a>", ">Test again</a>", ">Process</a>"} {
		if strings.Contains(page, gone) {
			t.Errorf("test page still has %s", gone)
		}
	}
	if !regexp.MustCompile(`<a class="pill ok" href="/admin/upstreams/tools/logs" title="[^"]*">running</a>`).MatchString(page) {
		t.Fatal("the running pill must link to the process page")
	}
	p := a.MCP.Managed.Lookup("tools")
	p.Call(t.Context(), "tools/call", map[string]any{"name": "log"})
	var logs string
	for end := time.Now().Add(5 * time.Second); !strings.Contains(logs, "fake log line from tool"); time.Sleep(50 * time.Millisecond) {
		if _, logs = br.get("/admin/upstreams/tools/logs"); time.Now().After(end) {
			t.Fatal("stderr must appear on the logs page")
		}
	}
}

func TestAdminImportExport(t *testing.T) {
	a, br, csrf := managedApp(t)
	doc := `{"mcpServers": {"files": {"command": "npx", "args": ["-y", "pkg"], "env": {"TOKEN": "import-secret-1234"}}, "remote": {"url": "https://mcp.example.com/mcp", "headers": {"X-Key": "hdr-secret-5678"}}, "bad": {}}}`
	r, page := br.post("/admin/upstreams/import", url.Values{"csrf": {csrf}, "json": {doc}, "include": {"1"}})
	if r.StatusCode != 200 || !strings.Contains(page, `class="pill ok">created`) || !strings.Contains(page, `class="pill bad">invalid`) {
		t.Fatalf("%d %.300s", r.StatusCode, page)
	}
	if strings.Contains(page, "import-secret-1234") && !strings.Contains(page, "<textarea") {
		t.Fatal("unexpected")
	}
	f, _ := a.MCP.Upstreams.Get("files")
	rem, _ := a.MCP.Upstreams.Get("remote")
	if f.Kind != mcp.KindStdio || f.Env[0].Value != "import-secret-1234" || f.IncludeInMCP || rem.Headers[0].Value != "hdr-secret-5678" {
		t.Fatalf("%+v %+v", f, rem)
	}
	// a second import reports conflicts
	_, page = br.post("/admin/upstreams/import", url.Values{"csrf": {csrf}, "json": {doc}})
	if !strings.Contains(page, `class="pill warn">skipped`) || !strings.Contains(page, "alias already exists") {
		t.Fatal("conflicts must be reported")
	}
	// document errors flash back
	if r, _ := br.post("/admin/upstreams/import", url.Values{"csrf": {csrf}, "json": {"{nope"}}); r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Fatal("bad JSON must flash an error")
	}
	// export: same shape, secrets left out
	resp, body := br.get("/admin/upstreams/export")
	if resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatal(resp.Header)
	}
	if strings.Contains(body, "import-secret-1234") || strings.Contains(body, "hdr-secret-5678") || !strings.Contains(body, `"mcpServers"`) || !strings.Contains(body, `"files"`) {
		t.Fatalf("export: %s", body)
	}
	// CSRF is enforced
	if r, _ := br.post("/admin/upstreams/import", url.Values{"json": {doc}}); r.StatusCode != 403 {
		t.Fatalf("import without CSRF: %d", r.StatusCode)
	}
	if r, _ := br.post("/admin/upstreams/files/process", url.Values{"action": {"start"}}); r.StatusCode != 403 {
		t.Fatalf("process without CSRF: %d", r.StatusCode)
	}
}

func TestAdminManagedRoutesRequireSession(t *testing.T) {
	_, ts, _, _ := signedIn(t, nil)
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/admin/upstreams/x/logs", "/admin/upstreams/import", "/admin/upstreams/export", "/admin/upstreams/x/process"} {
		resp, err := anon.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 302 && resp.StatusCode != 303 {
			t.Errorf("%s without a session: %d", p, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); !strings.Contains(loc, "/admin/oidc/login") {
			t.Errorf("%s redirects to %q", p, loc)
		}
	}
}

func TestAdminRemoteCustomHeadersRoundTripMasked(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	up := fakeUpstream(t)
	v := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"r"}, "url": {up.URL}, "auth_kind": {"none"}, "enabled": {"1"},
		"hdr_name": {"X-Api-Key", "X-Tenant", ""}, "hdr_value": {"header-secret-abcd", "t1", ""}}
	if r, _ := br.post("/admin/upstreams/save", v); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u, _ := a.MCP.Upstreams.Get("r")
	if len(u.Headers) != 2 || u.Headers[0].Value != "header-secret-abcd" {
		t.Fatalf("%+v", u.Headers)
	}
	_, edit := br.get("/admin/upstreams/r/edit")
	if strings.Contains(edit, "header-secret-abcd") || !strings.Contains(edit, "************abcd") {
		t.Fatal("header values must be masked on the edit page")
	}
	v["mode"] = []string{"edit"}
	v["hdr_value"] = []string{"************abcd", "t2", ""}
	br.post("/admin/upstreams/save", v)
	u, _ = a.MCP.Upstreams.Get("r")
	if u.Headers[0].Value != "header-secret-abcd" || u.Headers[1].Value != "t2" {
		t.Fatalf("%+v", u.Headers)
	}
}

// pillRE matches the shared status pill: class, hover text and the bare state word as its label.
func pillRE(class, label string) *regexp.Regexp {
	return regexp.MustCompile(`<span class="pill ` + class + `" title="[^"]*">` + label + `</span>`)
}

// The status is a bare pill everywhere (list, process page, Test page); pid, uptime, restarts and the
// last error live in its hover text only.
func TestProcessStatusIsAPillWithDetailsInTheTooltip(t *testing.T) {
	a, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "tools", url.Values{"lifecycle": {"always"}}))
	deadline := time.Now().Add(15 * time.Second)
	for a.MCP.Managed.Lookup("tools").Status().State != "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	pid := a.MCP.Managed.Lookup("tools").Status().PID
	_, list := br.get("/admin/upstreams")
	_, logs := br.get("/admin/upstreams/tools/logs")
	tips := map[string]*regexp.Regexp{
		"list":    regexp.MustCompile(`<a class="pill ok" href="/admin/upstreams/tools/logs" title="([^"]*)">running</a>`),
		"process": regexp.MustCompile(`<span class="pill ok" title="([^"]*)">running</span>`),
	}
	for name, page := range map[string]string{"list": list, "process": logs} {
		m := tips[name].FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("%s: no running pill", name)
		}
		if !strings.Contains(m[1], "pid "+itoa(int64(pid))) || !strings.Contains(m[1], "up ") {
			t.Errorf("%s: tooltip %q lacks pid and uptime", name, m[1])
		}
		if regexp.MustCompile(`(?i)>\s*(pid \d+|up \d)`).MatchString(page) {
			t.Errorf("%s: status details must not be visible text", name)
		}
	}
	for _, gone := range []string{"<th>PID</th>", "<th>Uptime</th>", "<th>Restarts</th>", "<th>Last error</th>"} {
		if strings.Contains(logs, gone) {
			t.Errorf("process page still has a %s row", gone)
		}
	}
	// a failed process: red pill, error only in the tooltip
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"stdio"}, "alias": {"broken"}, "command": {"/nonexistent/bin"},
		"lifecycle": {"always"}, "enabled": {"1"}, "include": {"1"}})
	deadline = time.Now().Add(10 * time.Second)
	for a.MCP.Managed.Lookup("broken").Status().State != "failed" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	_, list = br.get("/admin/upstreams")
	m := regexp.MustCompile(`<a class="pill bad" href="/admin/upstreams/broken/logs" title="([^"]*)">failed</a>`).FindStringSubmatch(list)
	if m == nil || !strings.Contains(m[1], "last error") || !strings.Contains(m[1], "no such file") {
		t.Fatalf("failed pill: %v", m)
	}
	if strings.Contains(strings.Replace(list, m[0], "", 1), "no such file") || strings.Contains(list, `class="sub bad"`) {
		t.Fatal("the last error must only be in the tooltip")
	}
}

// Host override sits in a closed "Advanced" block, open only when a value is stored; the header name
// field is shown by the auth type (data-when) and posts like any other field.
func TestRemoteFormAdvancedAndHeaderName(t *testing.T) {
	a, br, csrf := managedApp(t)
	a.Admin.NoSaveTest = true
	form := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"r"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"header"},
		"auth_name": {"X-Api-Key"}, "auth_value": {"k"}, "host_override": {"svc:8000"}, "enabled": {"1"}}
	if r, _ := br.post("/admin/upstreams/save", form); flashKind(r) != "ok" {
		t.Fatal(flashOf(r))
	}
	u, _ := a.MCP.Upstreams.Get("r")
	if u.AuthName != "X-Api-Key" || u.HostOverride != "svc:8000" {
		t.Fatalf("%+v", u)
	}
	_, edit := br.get("/admin/upstreams/r/edit")
	if !strings.Contains(edit, `<details open><summary class="muted">Advanced</summary>`) || !strings.Contains(edit, `data-when="header auto"`) {
		t.Fatal("stored host override must open Advanced; header name must be conditional")
	}
	_, list := br.get("/admin/upstreams/new")
	if !strings.Contains(list, `<details ><summary class="muted">Advanced</summary>`) {
		t.Fatal("new form must have a closed Advanced block")
	}
}

const inMCPHelp = "Only always-on servers can be in /mcp."

// The include control always comes with its explanation; for an on-demand managed server it is disabled, and the
// server never puts such a server on /mcp, however it was asked.
func TestIncludeInMCPControlExplainsAndEnforces(t *testing.T) {
	a, br, csrf := managedApp(t)
	_, add := br.get("/admin/upstreams/new")
	if !strings.Contains(add, inMCPHelp) || !strings.Contains(add, `name="include" value="1" data-include`) {
		t.Fatal("the add form must explain the include control")
	}
	br.post("/admin/upstreams/save", stdioForm(csrf, "od", nil))
	br.post("/admin/upstreams/save", stdioForm(csrf, "ao", url.Values{"lifecycle": {"always"}}))
	if u, _ := a.MCP.Upstreams.Get("od"); u.IncludeInMCP {
		t.Fatal("an on-demand managed upstream must not be on /mcp")
	}
	if u, _ := a.MCP.Upstreams.Get("ao"); !u.IncludeInMCP {
		t.Fatal("an always-on one may")
	}
	_, od := br.get("/admin/upstreams/od/edit")
	_, ao := br.get("/admin/upstreams/ao/edit")
	box := regexp.MustCompile(`<input type="checkbox" name="include"[^>]*>`)
	if !strings.Contains(od, inMCPHelp) || !strings.Contains(box.FindString(od), "disabled") || strings.Contains(box.FindString(od), "checked") {
		t.Errorf("on-demand edit page: %s", box.FindString(od))
	}
	if !strings.Contains(ao, inMCPHelp) || strings.Contains(box.FindString(ao), "disabled") || !strings.Contains(box.FindString(ao), "checked") {
		t.Errorf("always-on edit page: %s", box.FindString(ao))
	}
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, "on-demand servers are not on /mcp; only always-on servers can be") {
		t.Error("the list must explain the disabled toggle")
	}
	// the toggle endpoint refuses too, with the reason
	r, _ := br.post("/admin/upstreams/od/toggle", url.Values{"csrf": {csrf}, "flag": {"include"}})
	if _, msg := flashOf(r); !strings.Contains(msg, "always-on") {
		t.Errorf("toggle reason: %q", msg)
	}
}
