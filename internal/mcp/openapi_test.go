package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/openapi"
)

const oaSpec = `{"openapi":"3.0.0","info":{"title":"Notes API","version":"1"},
"paths":{"/notes":{"get":{"operationId":"listNotes","summary":"List notes","parameters":[{"name":"q","in":"query","schema":{"type":"string"}}]},
 "post":{"operationId":"addNote","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"text":{"type":"string"}}}}}}}},
 "/notes/{id}":{"delete":{"operationId":"deleteNote","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]}}}}`

type oaFake struct {
	*httptest.Server
	seen []string
}

func newOAFake(t *testing.T) *oaFake {
	f := &oaFake{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1}]`))
	}))
	t.Cleanup(f.Close)
	return f
}

func addOA(t *testing.T, e *env, alias, base string, enabled ...string) {
	t.Helper()
	if err := e.srv.Upstreams.Create(Upstream{Alias: alias, Kind: KindOpenAPI, URL: base, AuthKind: AuthBearer, AuthValue: "SECRETTOK", Enabled: true, IncludeInMCP: true}); err != nil {
		t.Fatal(err)
	}
	sel := openapi.Selection{Enabled: map[string]bool{}}
	for _, k := range enabled {
		sel.Enabled[k] = true
	}
	if err := e.srv.Upstreams.SetOpenAPI(alias, OpenAPIConfig{Spec: oaSpec, Selection: sel}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPIServedOnItsOwnEndpoint(t *testing.T) {
	e := newEnv(t, nil)
	api := newOAFake(t)
	addOA(t, e, "notes", api.URL, "GET /notes", "POST /notes")
	key, _, _ := e.keys.Create("t")

	_, m := e.rpc(key, "/mcp/notes", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	info := m["result"].(map[string]any)["serverInfo"].(map[string]any)
	if info["name"] != "Notes API" {
		t.Fatalf("initialize: %v", m)
	}
	_, m = e.rpc(key, "/mcp/notes", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if got := strings.Join(toolNames(t, m), ","); got != "listNotes,addNote" {
		t.Fatalf("tools = %q (a disabled operation must not be offered)", got)
	}
	_, m = e.rpc(key, "/mcp/notes", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"listNotes","arguments":{"q":"a b"}}}`)
	res := m["result"].(map[string]any)
	if res["isError"] != false || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), `[{"id":1}]`) {
		t.Fatalf("call: %v", m)
	}
	if len(api.seen) != 1 || api.seen[0] != "GET /notes?q=a+b auth=Bearer SECRETTOK" {
		t.Fatalf("API saw %v", api.seen)
	}
	// a disabled operation cannot be called even by name
	_, m = e.rpc(key, "/mcp/notes", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"deleteNote","arguments":{"id":"1"}}}`)
	if m["error"] == nil || len(api.seen) != 1 {
		t.Fatalf("disabled tool called: %v %v", m, api.seen)
	}
	// notifications get 202, GET is not supported
	if r := e.do("POST", "/mcp/notes", map[string]string{"Authorization": "Bearer " + key}, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.StatusCode != 202 {
		t.Errorf("notification: %d", r.StatusCode)
	}
	if r := e.do("GET", "/mcp/notes", map[string]string{"Authorization": "Bearer " + key}, ""); r.StatusCode != 405 {
		t.Errorf("GET: %d", r.StatusCode)
	}
	// a client key is still required
	if r := e.do("POST", "/mcp/notes", nil, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); r.StatusCode != 401 {
		t.Errorf("no key: %d", r.StatusCode)
	}
}

func TestOpenAPIInAggregateAndSelectionChange(t *testing.T) {
	e := newEnv(t, nil)
	api := newOAFake(t)
	addOA(t, e, "notes", api.URL, "GET /notes")
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if got := strings.Join(toolNames(t, m), ","); got != "notes-listNotes" {
		t.Fatalf("aggregate = %q", got)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"notes-listNotes"}}`)
	if m["result"] == nil || len(api.seen) != 1 {
		t.Fatalf("aggregate call: %v %v", m, api.seen)
	}
	// the selection changes at once (cache invalidated)
	st, _ := e.srv.Upstreams.OpenAPI("notes")
	cfg := st.Config
	cfg.Selection.Enabled = map[string]bool{"DELETE /notes/{id}": true}
	if err := e.srv.Upstreams.SetOpenAPI("notes", cfg); err != nil {
		t.Fatal(err)
	}
	_, m = e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if got := strings.Join(toolNames(t, m), ","); got != "notes-deleteNote" {
		t.Fatalf("after change = %q", got)
	}
	if n := e.srv.Upstreams.OpenAPIToolCount("notes"); n != 1 {
		t.Errorf("count %d", n)
	}
}

func TestOpenAPIUpstreamValidation(t *testing.T) {
	ok := Upstream{Alias: "a", Kind: KindOpenAPI, URL: "https://api.example.com/v1", AuthKind: AuthNone, Enabled: true}
	for name, mut := range map[string]func(*Upstream){
		"query without name":     func(u *Upstream) { u.AuthKind, u.AuthValue = AuthQuery, "v" },
		"basic with colon":       func(u *Upstream) { u.AuthKind, u.AuthName, u.AuthValue = AuthBasic, "a:b", "p" },
		"passthrough":            func(u *Upstream) { u.AuthKind = AuthPassthrough },
		"auto":                   func(u *Upstream) { u.AuthKind = AuthAuto },
		"credentials in the URL": func(u *Upstream) { u.URL = "https://u:p@api.example.com" },
		"a process":              func(u *Upstream) { u.Command = "x" },
	} {
		u := ok
		mut(&u)
		if u.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	u := ok
	u.AuthKind, u.AuthName, u.AuthValue = AuthQuery, "api_key", "v"
	if err := u.Validate(); err != nil {
		t.Errorf("query: %v", err)
	}
	// the remote kind still refuses the OpenAPI-only methods
	r := Upstream{Alias: "r", URL: "https://x.example.com", AuthKind: AuthQuery, AuthName: "k", AuthValue: "v"}
	if r.Validate() == nil {
		t.Error("remote accepted query auth")
	}
}

func TestOpenAPISecretSealedAndTested(t *testing.T) {
	e := newEnv(t, nil)
	api := newOAFake(t)
	addOA(t, e, "notes", api.URL, "GET /notes")
	var raw string
	if err := e.db.QueryRow(`SELECT auth_value FROM upstreams WHERE alias='notes'`).Scan(&raw); err != nil || strings.Contains(raw, "SECRETTOK") {
		t.Fatalf("secret stored in clear: %q %v", raw, err)
	}
	tr := e.srv.Test(t.Context(), "notes")
	if !tr.OK || tr.ToolTotal != 1 || len(api.seen) != 0 {
		t.Fatalf("test: %+v (no operation may be called by Test: %v)", tr, api.seen)
	}
	if err := e.srv.Upstreams.Delete("notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.srv.Upstreams.OpenAPI("notes"); err == nil {
		t.Error("description kept after delete")
	}
}

func TestToolLevel(t *testing.T) {
	for n, want := range map[int]string{0: "ok", 15: "ok", 16: "warn", 30: "warn", 31: "bad", 500: "bad"} {
		if ToolLevel(n) != want {
			t.Errorf("%d -> %s", n, ToolLevel(n))
		}
	}
}

func TestOpenAPIUpstreamsAreLeftOutOfTheJSONExport(t *testing.T) {
	ups := []Upstream{{Alias: "a", Kind: KindOpenAPI, URL: "https://api.example.com", Enabled: true},
		{Alias: "r", Kind: KindRemote, URL: "https://mcp.example.com/mcp", AuthKind: AuthNone, Enabled: true}}
	out := string(ExportJSON(ups))
	if strings.Contains(out, `"a"`) || !strings.Contains(out, `"r"`) {
		t.Errorf("export: %s", out)
	}
}
