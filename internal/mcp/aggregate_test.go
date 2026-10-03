package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// aggUp is a fake MCP server with sessions, tools, prompts and resources. It accepts one auth
// scheme ("bearer:<tok>" or "header:<tok>" or "none"), can answer as SSE and can be made to fail.
type aggUp struct {
	*httptest.Server
	mu       sync.Mutex
	inits    int
	sessions map[string]bool
	calls    []string // "<rpc method> sid=<id> host=<Host> auth=<kind>"
	tools    []string
	fail     string // "" | "500" | "rpcerr"
	sse      bool
	pages    bool // paginate tools/list in two pages
	forget   bool // next session id is forgotten once (404)
}

func newAggUp(t *testing.T, accept string, tools ...string) *aggUp {
	f := &aggUp{sessions: map[string]bool{}, tools: tools}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		kind := "none"
		switch {
		case strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
			kind = "bearer:" + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		case r.Header.Get("X-Api-Key") != "":
			kind = "header:" + r.Header.Get("X-Api-Key")
		}
		var m struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
			Params map[string]any  `json:"params"`
		}
		_ = json.Unmarshal(b, &m)
		sid := r.Header.Get("Mcp-Session-Id")
		f.mu.Lock()
		f.calls = append(f.calls, fmt.Sprintf("%s sid=%s host=%s auth=%s", m.Method, sid, r.Host, kind))
		known := f.sessions[sid]
		fail := f.fail
		f.mu.Unlock()
		if r.Method == http.MethodDelete {
			w.WriteHeader(200)
			return
		}
		if kind != accept {
			w.WriteHeader(401)
			return
		}
		reply := func(result any) {
			j, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "result": result})
			if f.sse {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", j)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(j)
		}
		if m.Method == "initialize" {
			f.mu.Lock()
			f.inits++
			id := fmt.Sprintf("s%d", f.inits)
			f.sessions[id] = true
			f.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", id)
			reply(map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "fake"}, "capabilities": map[string]any{"tools": map[string]any{}}})
			return
		}
		if m.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		f.mu.Lock()
		if f.forget {
			f.forget = false
			delete(f.sessions, sid)
			known = false
		}
		f.mu.Unlock()
		if !known {
			w.WriteHeader(404)
			return
		}
		if fail == "500" {
			w.WriteHeader(500)
			return
		}
		if fail == "rpcerr" {
			j, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "error": map[string]any{"code": -32000, "message": "boom"}})
			w.Header().Set("Content-Type", "application/json")
			w.Write(j)
			return
		}
		switch m.Method {
		case "tools/list":
			var list []any
			for _, n := range f.tools {
				list = append(list, map[string]any{"name": n, "description": "tool " + n, "inputSchema": map[string]any{"type": "object"}})
			}
			if f.pages {
				if m.Params["cursor"] == "p2" {
					reply(map[string]any{"tools": list[1:]})
				} else {
					reply(map[string]any{"tools": list[:1], "nextCursor": "p2"})
				}
				return
			}
			reply(map[string]any{"tools": list})
		case "tools/call":
			name, _ := m.Params["name"].(string)
			args, _ := json.Marshal(m.Params["arguments"])
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "called " + name + " " + string(args)}}})
		case "prompts/list":
			reply(map[string]any{"prompts": []any{map[string]any{"name": "greet", "description": "hi"}}})
		case "prompts/get":
			reply(map[string]any{"messages": []any{}, "description": fmt.Sprint(m.Params["name"])})
		case "resources/list":
			reply(map[string]any{"resources": []any{map[string]any{"uri": "file:///a.txt", "name": "a"}}})
		case "resources/read":
			reply(map[string]any{"contents": []any{map[string]any{"uri": m.Params["uri"], "text": "data"}}})
		default:
			j, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "error": map[string]any{"code": -32601, "message": "no"}})
			w.Header().Set("Content-Type", "application/json")
			w.Write(j)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *aggUp) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// rpc posts one JSON-RPC message to the aggregated /mcp endpoint and returns the decoded reply.
func (e *env) rpc(key, path, body string) (int, map[string]any) {
	e.t.Helper()
	r := e.do("POST", path, map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}, body)
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	var m map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			e.t.Fatalf("not json (%d): %s", r.StatusCode, b)
		}
	}
	return r.StatusCode, m
}

func toolNames(t *testing.T, m map[string]any) []string {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	var out []string
	for _, x := range res["tools"].([]any) {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

func TestAggregateInitializeAndToolsList(t *testing.T) {
	e := newEnv(t, nil)
	bb := newAggUp(t, "bearer:BBTOK", "list_children", "get_child")
	km := newAggUp(t, "header:KMTOK", "list_servers")
	km.sse = true // this one answers with SSE
	off := newAggUp(t, "none", "never_listed")
	excl := newAggUp(t, "none", "excluded")
	e.srv.Upstreams.Create(Upstream{Alias: "notes", URL: bb.URL, AuthKind: AuthBearer, AuthValue: "BBTOK", Enabled: true, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "ops", URL: km.URL, AuthKind: AuthAuto, AuthName: "X-Api-Key", AuthValue: "KMTOK", Enabled: true, IncludeInMCP: true, HostOverride: "localhost:9120"})
	e.srv.Upstreams.Create(Upstream{Alias: "disabled", URL: off.URL, AuthKind: AuthNone, Enabled: false, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "notincluded", URL: excl.URL, AuthKind: AuthNone, Enabled: true})
	key, _, _ := e.keys.Create("t")

	st, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
	res, _ := m["result"].(map[string]any)
	info, _ := res["serverInfo"].(map[string]any)
	caps, _ := res["capabilities"].(map[string]any)
	if st != 200 || info["name"] != "skgate" || res["protocolVersion"] != "2025-03-26" || caps["tools"] == nil {
		t.Fatalf("initialize: %d %v", st, m)
	}
	if bb.count("initialize") != 0 || km.count("initialize") != 0 {
		t.Fatal("initialize must be answered by skgate itself without touching upstreams")
	}

	st, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	got := strings.Join(toolNames(t, m), ",")
	if st != 200 || got != "notes-list_children,notes-get_child,ops-list_servers" {
		t.Fatalf("tools/list = %d %q", st, got)
	}
	if off.count("") != 0 || excl.count("") != 0 {
		t.Fatal("disabled or not included upstreams were contacted")
	}
	// the tool definition is otherwise untouched
	first := m["result"].(map[string]any)["tools"].([]any)[0].(map[string]any)
	if first["description"] != "tool list_children" || first["inputSchema"] == nil {
		t.Fatalf("tool fields lost: %v", first)
	}
	// auth kinds and host override: bearer for one, auto-detected header for the other
	if !strings.Contains(strings.Join(bb.calls, "\n"), "tools/list sid=s1") || !strings.Contains(strings.Join(bb.calls, "\n"), "auth=bearer:BBTOK") {
		t.Fatalf("notes calls: %v", bb.calls)
	}
	got2, _ := e.srv.Upstreams.Get("ops")
	if got2.DetectedKind != "header" {
		t.Fatalf("auto detection not used/persisted: %q %s", got2.DetectedKind, got2.DetectedNote)
	}
	if !strings.Contains(km.calls[len(km.calls)-1], "host=localhost:9120") || !strings.Contains(km.calls[len(km.calls)-1], "auth=header:KMTOK") {
		t.Fatalf("ops last call lacks host override or header auth: %v", km.calls)
	}

	// sessions are kept: a second list does not initialize again
	// (ops has one extra initialize from the one-time auto-detection probe)
	kmBefore := km.inits
	e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if bb.inits != 1 || km.inits != kmBefore {
		t.Fatalf("upstream sessions must be reused: bb=%d km=%d (was %d)", bb.inits, km.inits, kmBefore)
	}
	// /mcp/ with trailing slash and the legacy alias-less path behave the same
	if st, m = e.rpc(key, "/mcp/", `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`); st != 200 || len(toolNames(t, m)) != 3 {
		t.Fatalf("/mcp/: %d %v", st, m)
	}
}

func TestAggregateToolsCallRoutesAndStripsPrefix(t *testing.T) {
	e := newEnv(t, nil)
	bb := newAggUp(t, "bearer:BBTOK", "list_children")
	km := newAggUp(t, "header:KMTOK", "list_servers")
	// aliases that share a dash prefix: the longest match must win
	bb2 := newAggUp(t, "none", "x")
	e.srv.Upstreams.Create(Upstream{Alias: "notes", URL: bb.URL, AuthKind: AuthBearer, AuthValue: "BBTOK", Enabled: true, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "kom", URL: km.URL, AuthKind: AuthHeader, AuthName: "X-Api-Key", AuthValue: "KMTOK", Enabled: true, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "kom-odo", URL: bb2.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")

	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"notes-list_children","arguments":{"limit":2}}}`)
	res, _ := m["result"].(map[string]any)
	txt := res["content"].([]any)[0].(map[string]any)["text"]
	if m["id"] != float64(7) || txt != `called list_children {"limit":2}` {
		t.Fatalf("call result: %v", m)
	}
	if !strings.Contains(strings.Join(bb.calls, "\n"), "tools/call") || !strings.Contains(strings.Join(bb.calls, "\n"), "auth=bearer:BBTOK") || km.count("tools/call") != 0 {
		t.Fatalf("routing: bb=%v km=%v", bb.calls, km.calls)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"kom-list_servers"}}`)
	if txt := m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]; !strings.HasPrefix(fmt.Sprint(txt), "called list_servers") {
		t.Fatalf("kom call: %v", m)
	}
	if !strings.Contains(strings.Join(km.calls, "\n"), "auth=header:KMTOK") {
		t.Fatalf("kom auth: %v", km.calls)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"kom-odo-x"}}`)
	if !strings.HasPrefix(fmt.Sprint(m["result"]), "map[content:") || bb2.count("tools/call") != 1 || km.count("tools/call") != 1 {
		t.Fatalf("longest alias must win: %v bb2=%v", m, bb2.calls)
	}
	// unknown prefix and missing name are JSON-RPC errors, not HTTP failures
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"nosuch-tool"}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"plainname"}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{}}`,
	} {
		st, m := e.rpc(key, "/mcp", body)
		er, _ := m["error"].(map[string]any)
		if st != 200 || er == nil || er["code"] != float64(-32602) {
			t.Fatalf("%s -> %d %v", body, st, m)
		}
	}
	// the upstream's own JSON-RPC error is passed through unchanged
	km.mu.Lock()
	km.fail = "rpcerr"
	km.mu.Unlock()
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"kom-list_servers"}}`)
	if er, _ := m["error"].(map[string]any); er == nil || er["message"] != "boom" || m["id"] != float64(13) {
		t.Fatalf("upstream error not passed through: %v", m)
	}
	// an upstream that is down gives an error for that call only, without credentials in it
	km.mu.Lock()
	km.fail = "500"
	km.mu.Unlock()
	st, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"kom-list_servers"}}`)
	er, _ := m["error"].(map[string]any)
	if st != 200 || er == nil || !strings.Contains(fmt.Sprint(er["message"]), "HTTP 500") || strings.Contains(fmt.Sprint(m), "KMTOK") {
		t.Fatalf("failed upstream call: %d %v", st, m)
	}
}

func TestAggregateSkipsFailingUpstreamAndLogsReason(t *testing.T) {
	e := newEnv(t, nil)
	ok := newAggUp(t, "none", "fine")
	bad := newAggUp(t, "none", "broken")
	bad.fail = "500"
	denied := newAggUp(t, "bearer:right", "denied")
	rpcerr := newAggUp(t, "none", "weird")
	rpcerr.fail = "rpcerr"
	e.srv.Upstreams.Create(Upstream{Alias: "a-ok", URL: ok.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "b-bad", URL: bad.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "c-denied", URL: denied.URL, AuthKind: AuthBearer, AuthValue: "WRONGSECRET", Enabled: true, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "d-rpc", URL: rpcerr.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	e.srv.Upstreams.Create(Upstream{Alias: "e-gone", URL: "http://127.0.0.1:1/mcp", AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	st, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if st != 200 || strings.Join(toolNames(t, m), ",") != "a-ok-fine" {
		t.Fatalf("expected only the healthy upstream: %d %v", st, m)
	}
	logs := e.logs.String()
	for _, want := range []string{"alias=b-bad", "HTTP 500", "alias=c-denied", "rejected the outbound credentials", "alias=d-rpc", "JSON-RPC error -32000", "alias=e-gone", "unreachable"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "WRONGSECRET") || strings.Contains(logs, "http://") {
		t.Errorf("log leaks a credential or a URL:\n%s", logs)
	}
	// with nothing included the list is simply empty
	for _, a := range []string{"a-ok", "b-bad", "c-denied", "d-rpc", "e-gone"} {
		u, _ := e.srv.Upstreams.Get(a)
		u.IncludeInMCP = false
		e.srv.Upstreams.Update(u, true)
	}
	st, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if st != 200 || len(m["result"].(map[string]any)["tools"].([]any)) != 0 {
		t.Fatalf("empty aggregate: %d %v", st, m)
	}
}

func TestAggregatePaginationSessionRecoveryAndSSE(t *testing.T) {
	e := newEnv(t, nil)
	pg := newAggUp(t, "none", "one", "two")
	pg.pages = true
	pg.sse = true
	e.srv.Upstreams.Create(Upstream{Alias: "pg", URL: pg.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if got := strings.Join(toolNames(t, m), ","); got != "pg-one,pg-two" {
		t.Fatalf("paginated SSE list: %q", got)
	}
	// the upstream forgets the session (404): skgate re-initializes once and retries
	pg.mu.Lock()
	pg.forget = true
	pg.mu.Unlock()
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"pg-one","arguments":{}}}`)
	if m["result"] == nil || pg.inits != 2 {
		t.Fatalf("session recovery: inits=%d %v", pg.inits, m)
	}
	// editing the upstream (new credentials) never reuses the old session
	u, _ := e.srv.Upstreams.Get("pg")
	u.HostOverride = "other:1"
	e.srv.Upstreams.Update(u, true)
	e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if pg.inits != 3 {
		t.Fatalf("changed upstream must start a new session, inits=%d", pg.inits)
	}
}

func TestAggregatePromptsResourcesAndFallbacks(t *testing.T) {
	e := newEnv(t, nil)
	a := newAggUp(t, "none", "t")
	e.srv.Upstreams.Create(Upstream{Alias: "a", URL: a.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`)
	p := m["result"].(map[string]any)["prompts"].([]any)[0].(map[string]any)
	if p["name"] != "a-greet" {
		t.Fatalf("prompts/list: %v", m)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"a-greet"}}`)
	if m["result"].(map[string]any)["description"] != "greet" {
		t.Fatalf("prompts/get strips prefix: %v", m)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	r := m["result"].(map[string]any)["resources"].([]any)[0].(map[string]any)
	if r["uri"] != "a+file:///a.txt" || r["name"] != "a-a" {
		t.Fatalf("resources/list: %v", r)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"a+file:///a.txt"}}`)
	c := m["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
	if c["uri"] != "a+file:///a.txt" || c["text"] != "data" {
		t.Fatalf("resources/read: %v", c)
	}
	// resource templates: an upstream without the method contributes nothing (empty list)
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":5,"method":"resources/templates/list"}`)
	if l := m["result"].(map[string]any)["resourceTemplates"].([]any); len(l) != 0 {
		t.Fatalf("templates: %v", m)
	}
	// ping, unknown methods, notifications, batches, garbage
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":6,"method":"ping"}`)
	if m["result"] == nil {
		t.Fatalf("ping: %v", m)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":7,"method":"completion/complete"}`)
	if er, _ := m["error"].(map[string]any); er == nil || er["code"] != float64(-32601) {
		t.Fatalf("unknown method: %v", m)
	}
	if st, _ := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); st != 202 {
		t.Fatalf("notification: %d", st)
	}
	r2 := e.do("POST", "/mcp", map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"},
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/x"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`)
	var batch []map[string]any
	b, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if json.Unmarshal(b, &batch) != nil || len(batch) != 2 || batch[1]["id"] != float64(2) {
		t.Fatalf("batch: %s", b)
	}
	if st, m := e.rpc(key, "/mcp", `not json`); st != 400 || m["error"] == nil {
		t.Fatalf("garbage: %d %v", st, m)
	}
	// inbound auth still required, GET has no stream, DELETE is a no-op
	if r := e.do("POST", "/mcp", nil, `{}`); r.StatusCode != 401 {
		t.Fatalf("unauthenticated aggregate: %d", r.StatusCode)
	}
	if r := e.do("GET", "/mcp", map[string]string{"Authorization": "Bearer " + key}, ""); r.StatusCode != 405 {
		t.Fatalf("GET /mcp: %d", r.StatusCode)
	}
	if r := e.do("DELETE", "/mcp", map[string]string{"Authorization": "Bearer " + key}, ""); r.StatusCode != 204 {
		t.Fatalf("DELETE /mcp: %d", r.StatusCode)
	}
	// a single upstream stays transparent and unprefixed
	r3 := e.do("POST", "/mcp/a", map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	b, _ = io.ReadAll(r3.Body)
	r3.Body.Close()
	if !strings.Contains(string(b), `"name":"fake"`) {
		t.Fatalf("/mcp/a must proxy transparently: %s", b)
	}
}

func TestAggregateLegacySSERoot(t *testing.T) {
	e := newEnv(t, nil)
	a := newAggUp(t, "none", "t1")
	e.srv.Upstreams.Create(Upstream{Alias: "a", URL: a.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	req, _ := http.NewRequest("GET", e.ts.URL+"/sse", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/sse: %d", resp.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	ep := strings.TrimSpace(strings.SplitN(strings.SplitN(string(buf[:n]), "data: ", 2)[1], "\n", 2)[0])
	pr := e.do("POST", ep, map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`)
	pr.Body.Close()
	if pr.StatusCode != 202 {
		t.Fatalf("POST %s: %d", ep, pr.StatusCode)
	}
	n, _ = resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), `a-t1`) || !strings.Contains(string(buf[:n]), `"id":5`) {
		t.Fatalf("SSE reply: %s", buf[:n])
	}
}

// What a client is told about a dead upstream names no address.
func TestAggregateCallToDeadUpstreamNamesNoAddress(t *testing.T) {
	e := newEnv(t, nil)
	addr := deadAddr(t)
	e.srv.Upstreams.Create(Upstream{Alias: "gone", URL: "http://" + addr + "/mcp", AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"gone-any"}}`)
	er, _ := m["error"].(map[string]any)
	msg := fmt.Sprint(er["message"])
	host, port, _ := net.SplitHostPort(addr)
	if er == nil || !strings.Contains(msg, "connection refused") || strings.Contains(msg, host) || strings.Contains(msg, port) {
		t.Fatalf("client-facing error: %v", m)
	}
}
