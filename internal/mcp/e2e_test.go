package mcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
)

// Real-runtime smoke tests: set SKGATE_E2E=1 (needs network, node/npx and uv/uvx). They run real
// MCP servers through skgate's HTTP endpoints.
func e2eEnv(t *testing.T) *env {
	if os.Getenv("SKGATE_E2E") != "1" {
		t.Skip("set SKGATE_E2E=1 to run real npx/uvx servers")
	}
	home := t.TempDir()
	t.Setenv("NPM_CONFIG_CACHE", os.Getenv("SKGATE_E2E_CACHE")+"/npm")
	t.Setenv("UV_CACHE_DIR", os.Getenv("SKGATE_E2E_CACHE")+"/uv")
	_ = home
	return newEnv(t, func(c *config.Config) {
		c.ManagedDir, c.ManagedMaxProcs = t.TempDir(), 4
		c.ManagedStopGrace, c.ManagedLogLines, c.ManagedInstallMax = 5*time.Second, 500, 5*time.Minute
	})
}

func e2eSession(t *testing.T, e *env, key, alias string) map[string]string {
	t.Helper()
	hdr := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	r := e.do("POST", "/mcp/"+alias, hdr, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}`)
	body := readBody(r)
	if r.StatusCode != 200 || !strings.Contains(body, `"serverInfo"`) {
		t.Fatalf("%s initialize: %d %s", alias, r.StatusCode, body)
	}
	t.Logf("%s initialize: %.200s", alias, body)
	hdr["Mcp-Session-Id"] = r.Header.Get("Mcp-Session-Id")
	e.do("POST", "/mcp/"+alias, hdr, `{"jsonrpc":"2.0","method":"notifications/initialized"}`).Body.Close()
	return hdr
}

func TestE2ENpxServerEverything(t *testing.T) {
	e := e2eEnv(t)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "everything", Kind: KindStdio, Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-everything"},
		Enabled: true, IncludeInMCP: true, StartupSecs: 240}); err != nil {
		t.Fatal(err)
	}
	key, _, _ := e.keys.Create("t")
	hdr := e2eSession(t, e, key, "everything")
	m := readJSON(t, e.do("POST", "/mcp/everything", hdr, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	names := toolNames(t, m)
	t.Logf("tools: %v", names)
	if len(names) < 5 {
		t.Fatalf("too few tools: %v", names)
	}
	echo := ""
	for _, n := range names {
		if n == "echo" {
			echo = n
		}
	}
	if echo == "" {
		t.Fatalf("no echo tool in %v", names)
	}
	out := readBody(e.do("POST", "/mcp/everything", hdr, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello skgate"}}}`))
	if !strings.Contains(out, "hello skgate") {
		t.Fatalf("echo: %s", out)
	}
	// aggregator: prefixed
	_, agg := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	if got := strings.Join(toolNames(t, agg), ","); !strings.Contains(got, "everything-echo") {
		t.Fatalf("aggregate: %s", got)
	}
	_, agg = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"everything-echo","arguments":{"message":"via aggregate"}}}`)
	if b, _ := json.Marshal(agg); !strings.Contains(string(b), "via aggregate") {
		t.Fatalf("%s", b)
	}
	// long running tool with progress over SSE
	sse := map[string]string{}
	for k, v := range hdr {
		sse[k] = v
	}
	sse["Accept"] = "text/event-stream"
	body := readBody(e.do("POST", "/mcp/everything", sse, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"trigger-long-running-operation","arguments":{"duration":2,"steps":3},"_meta":{"progressToken":"p1"}}}`))
	t.Logf("long-running (SSE): %.400s", body)
	if !strings.Contains(body, `"id":6`) {
		t.Fatalf("no final response: %s", body)
	}
	if !strings.Contains(body, "notifications/progress") || !strings.Contains(body, `"progressToken":"p1"`) {
		t.Errorf("progress notifications with the client's token expected in: %s", body)
	}
	tr := e.srv.Test(t.Context(), "everything")
	if !tr.OK {
		t.Fatalf("%+v", tr)
	}
	t.Logf("Test button: server=%q protocol=%s tools=%d", tr.Server, tr.Protocol, tr.ToolTotal)
	pid := e.srv.Managed.Lookup("everything").Status().PID
	e.srv.ShutdownManaged()
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat("/proc/" + itoaT(pid)); err == nil {
		t.Fatalf("npx server pid %d survived shutdown", pid)
	}
}

func TestE2EUvxServer(t *testing.T) {
	e := e2eEnv(t)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "clock", Kind: KindStdio, Command: "uvx", Args: []string{"mcp-server-time"},
		Enabled: true, IncludeInMCP: true, StartupSecs: 240}); err != nil {
		t.Fatal(err)
	}
	key, _, _ := e.keys.Create("t")
	hdr := e2eSession(t, e, key, "clock")
	m := readJSON(t, e.do("POST", "/mcp/clock", hdr, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	names := toolNames(t, m)
	t.Logf("tools: %v", names)
	out := readBody(e.do("POST", "/mcp/clock", hdr, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_current_time","arguments":{"timezone":"UTC"}}}`))
	if !strings.Contains(out, "UTC") {
		t.Fatalf("get_current_time: %s", out)
	}
	t.Logf("get_current_time: %.300s", out)
	if tr := e.srv.Test(t.Context(), "clock"); !tr.OK {
		t.Fatalf("%+v", tr)
	}
}
