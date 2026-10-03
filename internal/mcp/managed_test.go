package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
)

func managedEnv(t *testing.T) *env {
	return newEnv(t, func(c *config.Config) {
		c.ManagedDir, c.ManagedMaxProcs = t.TempDir(), 4
		c.ManagedStopGrace, c.ManagedLogLines, c.ManagedInstallMax = 2*time.Second, 200, 20*time.Second
	})
}

func addFake(t *testing.T, e *env, alias string, mut func(*Upstream)) {
	t.Helper()
	u := Upstream{Alias: alias, Kind: KindStdio, Command: os.Args[0], Env: []KV{{"FAKE_MCP", "1"}}, Enabled: true, IncludeInMCP: true, StartupSecs: 15}
	if mut != nil {
		mut(&u)
	}
	if err := e.srv.Upstreams.Create(u); err != nil {
		t.Fatal(err)
	}
}

func TestManagedPerAliasIsTransparentAndUnprefixed(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "tools", nil)
	key, _, _ := e.keys.Create("t")
	// no key: same 401 as any upstream
	if r := e.do("POST", "/mcp/tools", nil, `{}`); r.StatusCode != 401 {
		t.Fatalf("unauthenticated: %d", r.StatusCode)
	}
	hdr := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	r := e.do("POST", "/mcp/tools", hdr, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t"}}}`)
	sid := r.Header.Get("Mcp-Session-Id")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || sid == "" || !strings.Contains(string(b), "fake-stdio") {
		t.Fatalf("initialize: %d %s", r.StatusCode, b)
	}
	if r.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("CORS headers missing")
	}
	hdr["Mcp-Session-Id"] = sid
	code, m := e.rpc(key, "/mcp/tools", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	_ = code
	// rpc() has no session header; use do() for session-bound calls
	r = e.do("POST", "/mcp/tools", hdr, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	m = readJSON(t, r)
	names := toolNames(t, m)
	if strings.Join(names, ",") != "echo,slow,progress" {
		t.Fatalf("per-alias tools must be unprefixed: %v", names)
	}
	r = e.do("POST", "/mcp/tools/", hdr, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	if r.StatusCode != 200 || !strings.Contains(readBody(r), `"text":"hi"`) {
		t.Fatalf("trailing slash on the managed route must behave the same, got %d", r.StatusCode)
	}
	// the process is not restarted for another session
	st := e.srv.Managed.Lookup("tools").Status()
	if st.State != "running" || st.Restarts != 0 {
		t.Fatalf("%+v", st)
	}
	// DELETE ends the session
	r = e.do("DELETE", "/mcp/tools", hdr, "")
	if r.StatusCode != 204 {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if r := e.do("PUT", "/mcp/tools", hdr, ""); r.StatusCode != 405 {
		t.Fatalf("PUT: %d", r.StatusCode)
	}
}

func readBody(r *http.Response) string {
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func TestManagedDisabledUpstreamAndUnknownAliasAre404(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "off", func(u *Upstream) { u.Enabled = false })
	key, _, _ := e.keys.Create("t")
	hdr := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}
	if r := e.do("POST", "/mcp/off", hdr, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != 404 {
		t.Fatalf("disabled: %d", r.StatusCode)
	}
	if e.srv.Managed.Lookup("off") != nil {
		t.Fatal("a disabled upstream must never get a process")
	}
}

func TestManagedAggregatorPrefixesAndRoutes(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "mgd", func(u *Upstream) { u.Lifecycle = "always" })
	remote := newAggUp(t, "none", "rt")
	e.srv.Upstreams.Create(Upstream{Alias: "rem", URL: remote.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	got := strings.Join(toolNames(t, m), ",")
	for _, want := range []string{"mgd-echo", "mgd-slow", "mgd-progress", "rem-rt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mgd-echo","arguments":{"text":"via aggregate"}}}`)
	if !strings.Contains(string(mustJSONt(m)), "via aggregate") {
		t.Fatalf("%v", m)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if !strings.Contains(string(mustJSONt(m)), "fake.txt") {
		t.Fatalf("resources: %v", m)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":4,"method":"prompts/list"}`)
	if !strings.Contains(string(mustJSONt(m)), "mgd-greet") {
		t.Fatalf("prompts: %v", m)
	}
	// the aggregator's private calls never create client sessions on the process
	if s := e.srv.Managed.Lookup("mgd").Status(); s.Sessions != 0 || s.State != "running" {
		t.Fatalf("%+v", s)
	}
}

func mustJSONt(v any) []byte { b, _ := json.Marshal(v); return b }

func TestManagedAggregatorSkipsUnavailableManagedUpstream(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "good", func(u *Upstream) { u.Lifecycle = "always" })
	addFake(t, e, "broken", func(u *Upstream) { u.Lifecycle = "always"; u.Command = "definitely-not-installed-xyz"; u.Env = nil })
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	got := strings.Join(toolNames(t, m), ",")
	if !strings.Contains(got, "good-echo") || strings.Contains(got, "broken-") {
		t.Fatalf("%s", got)
	}
}

// slimEdition makes the build behave like the slim image for the test's duration.
func slimEdition(t *testing.T) {
	old := config.Edition
	config.Edition = "slim"
	t.Cleanup(func() { config.Edition = old })
}

func TestSlimEditionNeverSpawns(t *testing.T) {
	slimEdition(t)
	e := newEnv(t, func(c *config.Config) { c.ManagedDir = t.TempDir() })
	marker := t.TempDir() + "/spawned"
	e.srv.Upstreams.Create(Upstream{Alias: "m", Kind: KindStdio, Command: "/bin/sh", Args: []string{"-c", "touch " + marker}, Enabled: true, IncludeInMCP: true, Lifecycle: "always"})
	e.srv.StartManaged()
	e.srv.SyncManaged("m")
	key, _, _ := e.keys.Create("t")
	hdr := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}
	r := e.do("POST", "/mcp/m", hdr, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	body := readBody(r)
	if r.StatusCode != 503 || !strings.Contains(body, "slim image") {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if len(toolNames(t, m)) != 0 {
		t.Fatalf("%v", m)
	}
	if tr := e.srv.Test(t.Context(), "m"); tr.OK || !strings.Contains(tr.Error, "slim image") {
		t.Fatalf("%+v", tr)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a managed command ran in the slim edition")
	}
	if ok, why := e.srv.ManagedState(); ok || !strings.Contains(why, "slim") {
		t.Fatal(why)
	}
}

func TestFullEditionNeedsNoSwitch(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ManagedDir = t.TempDir() })
	if ok, why := e.srv.ManagedState(); !ok {
		t.Fatalf("managed must be available in the full edition: %s", why)
	}
}

func TestManagedTestButton(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "tools", nil)
	tr := e.srv.Test(t.Context(), "tools")
	if !tr.OK || tr.ToolTotal != 3 || tr.Server != "fake-stdio 1.0" || tr.Protocol == "" || tr.Auth != "managed" {
		t.Fatalf("%+v", tr)
	}
	addFake(t, e, "bad", func(u *Upstream) { u.Env = append(u.Env, KV{"FAKE_CRASH_ON_START", "1"}); u.StartupSecs = 5 })
	tr = e.srv.Test(t.Context(), "bad")
	if tr.OK || !strings.Contains(tr.Error, "crashing on start") {
		t.Fatalf("%+v", tr)
	}
}

func TestManagedSyncFollowsEditsAndDeletes(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "s", func(u *Upstream) { u.Lifecycle = "always"; u.Env = append(u.Env, KV{"FAKE_NAME", "one"}) })
	e.srv.SyncManaged("s")
	p := e.srv.Managed.Lookup("s")
	deadline := time.Now().Add(10 * time.Second)
	for p.Status().State != "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if p.Status().State != "running" {
		t.Fatalf("always-on upstream did not start: %+v", p.Status())
	}
	pid := p.Status().PID
	up, _ := e.srv.Upstreams.Get("s")
	up.Env = []KV{{"FAKE_MCP", "1"}, {"FAKE_NAME", "two"}}
	if err := e.srv.Upstreams.Update(up, false); err != nil {
		t.Fatal(err)
	}
	e.srv.SyncManaged("s")
	deadline = time.Now().Add(10 * time.Second)
	for (p.Status().State != "running" || p.Status().PID == pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(p.InitResult()), "two") {
		t.Fatalf("edit not applied: %s", p.InitResult())
	}
	newPid := p.Status().PID
	e.srv.Upstreams.SetFlag("s", FlagEnabled, false)
	e.srv.SyncManaged("s")
	if e.srv.Managed.Lookup("s") != nil {
		t.Fatal("disabling must stop and forget the process")
	}
	waitGone(t, newPid)
	e.srv.Upstreams.SetFlag("s", FlagEnabled, true)
	e.srv.SyncManaged("s")
	e.srv.Upstreams.Delete("s")
	e.srv.SyncManaged("s")
	if e.srv.Managed.Lookup("s") != nil {
		t.Fatal("delete must forget the process")
	}
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/proc/" + itoaT(pid)); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still exists", pid)
}

func itoaT(n int) string { return string(mustJSONt(n)) }

func TestManagedLegacySSEBridge(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "tools", nil)
	key, _, _ := e.keys.Create("t")
	req, _ := http.NewRequest("GET", e.ts.URL+"/sse/tools", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("/sse/tools: %v", err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	var ep string
	for ep == "" {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if d, ok := strings.CutPrefix(line, "data: "); ok {
			ep = strings.TrimSpace(d)
		}
	}
	var mu sync.Mutex
	msgs := map[string]string{}
	go func() {
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			if d, ok := strings.CutPrefix(line, "data: "); ok && strings.Contains(d, `"id"`) {
				var m struct{ ID json.RawMessage }
				json.Unmarshal([]byte(d), &m)
				mu.Lock()
				msgs[string(m.ID)] = d
				mu.Unlock()
			}
		}
	}()
	post := func(body string) {
		r := e.do("POST", ep, map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}, body)
		r.Body.Close()
		if r.StatusCode != 202 {
			t.Fatalf("POST: %d", r.StatusCode)
		}
	}
	post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`)
	post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"legacy"}}}`)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		a, b := msgs["1"], msgs["2"]
		mu.Unlock()
		if a != "" && b != "" {
			if !strings.Contains(a, "fake-stdio") || !strings.Contains(b, "legacy") {
				t.Fatalf("%s | %s", a, b)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no replies on the SSE stream: %v", msgs)
}

func TestManagedUpstreamNeverGetsRemoteBehaviour(t *testing.T) {
	e := managedEnv(t)
	addFake(t, e, "tools", nil)
	up, _ := e.srv.Upstreams.Get("tools")
	if up.URL != "" || up.DetectedKind != "" {
		t.Fatalf("%+v", up)
	}
	key, _, _ := e.keys.Create("t")
	e.do("POST", "/mcp/tools", map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json"},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`).Body.Close()
	up, _ = e.srv.Upstreams.Get("tools")
	if up.DetectedKind != "" || up.DetectedNote != "" {
		t.Fatalf("no detection or slash learning for managed upstreams: %+v", up)
	}
}
