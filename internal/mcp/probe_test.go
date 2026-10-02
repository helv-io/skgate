package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// authMCP is a fake MCP upstream that accepts exactly one auth scheme.
type authMCP struct {
	*httptest.Server
	mu    sync.Mutex
	hosts []string
	calls []string // "<method> <rpc method> auth=<none|bearer|header>"
	sse   bool
	tools string
}

func newAuthMCP(t *testing.T, accept string, sse bool) *authMCP {
	f := &authMCP{sse: sse, tools: `[{"name":"alpha","description":"Does alpha.\nSecond line hidden."},{"name":"beta","description":""}]`}
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
		}
		_ = json.Unmarshal(b, &m)
		f.mu.Lock()
		f.hosts = append(f.hosts, r.Host)
		f.calls = append(f.calls, r.Method+" "+m.Method+" auth="+kind)
		f.mu.Unlock()
		if r.Method == http.MethodDelete {
			w.WriteHeader(200)
			return
		}
		if accept == "oauth" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://up.example/.well-known/oauth-protected-resource"`)
			w.WriteHeader(401)
			return
		}
		if accept == "forbidden" {
			w.WriteHeader(403)
			return
		}
		if accept == "garbage" {
			io.WriteString(w, "<html>hello</html>")
			return
		}
		if accept == "rpcerr" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"nope"}}`)
			return
		}
		if kind != accept {
			w.WriteHeader(401)
			return
		}
		reply := func(v any) {
			j, _ := json.Marshal(v)
			if f.sse {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, ": comment\nevent: message\ndata: %s\n\n", j)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(j)
		}
		switch m.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-42")
			reply(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "fake", "version": "1.2"}}})
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != "sess-42" {
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(202)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "sess-42" {
				reply(map[string]any{"jsonrpc": "2.0", "id": 2, "error": map[string]any{"code": -32000, "message": "no session"}})
				return
			}
			var tools any
			_ = json.Unmarshal([]byte(f.tools), &tools)
			reply(map[string]any{"jsonrpc": "2.0", "id": 2, "result": map[string]any{"tools": tools}})
		default:
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *authMCP) callLog() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func TestDetectOrderAndPersistence(t *testing.T) {
	cases := []struct {
		name, accept string
		up           Upstream
		want         string
		wantNote     []string
		notCalled    []string
	}{
		{"none wins first", "none", Upstream{AuthValue: "TOK", AuthName: "X-Api-Key"}, "none", []string{"none: HTTP 200"}, []string{"auth=bearer", "auth=header"}},
		{"bearer second", "bearer:TOK", Upstream{AuthValue: "TOK", AuthName: "X-Api-Key"}, "bearer", []string{"none: HTTP 401", "bearer: HTTP 200"}, []string{"auth=header"}},
		{"header third", "header:TOK", Upstream{AuthValue: "TOK", AuthName: "X-Api-Key"}, "header", []string{"none: HTTP 401", "bearer: HTTP 401", "header: HTTP 200"}, nil},
		{"header skipped without a name", "header:TOK", Upstream{AuthValue: "TOK"}, DetectedFailed, []string{"header: skipped (no header name given)"}, []string{"auth=header"}},
		{"bearer skipped without credential", "bearer:TOK", Upstream{}, DetectedFailed, []string{"bearer: skipped (no credential stored)"}, []string{"auth=bearer"}},
		{"oauth advertised", "oauth", Upstream{AuthValue: "TOK"}, DetectedOAuth, []string{"advertises OAuth"}, nil},
		{"403 is failure", "forbidden", Upstream{AuthValue: "TOK", AuthName: "X-Api-Key"}, DetectedFailed, []string{"HTTP 403"}, nil},
		{"garbage body is not MCP", "garbage", Upstream{}, DetectedFailed, []string{"not a JSON-RPC 2.0 message"}, nil},
		{"rpc error is not success", "rpcerr", Upstream{}, DetectedFailed, []string{"JSON-RPC error -32600"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			f := newAuthMCP(t, c.accept, false)
			c.up.Alias, c.up.URL, c.up.AuthKind, c.up.Enabled = "u", f.URL, AuthAuto, true
			if err := e.srv.Upstreams.Create(c.up); err != nil {
				t.Fatal(err)
			}
			up, err := e.srv.Redetect(t.Context(), "u")
			if err != nil {
				t.Fatal(err)
			}
			if up.DetectedKind != c.want {
				t.Fatalf("detected %q (%s), want %q", up.DetectedKind, up.DetectedNote, c.want)
			}
			for _, n := range c.wantNote {
				if !strings.Contains(up.DetectedNote, n) {
					t.Errorf("note lacks %q: %s", n, up.DetectedNote)
				}
			}
			if strings.Contains(up.DetectedNote, "TOK") {
				t.Errorf("note leaks the credential: %s", up.DetectedNote)
			}
			for _, n := range c.notCalled {
				if strings.Contains(f.callLog(), n) {
					t.Errorf("unexpected probe %q:\n%s", n, f.callLog())
				}
			}
			got, _ := e.srv.Upstreams.Get("u")
			if got.DetectedKind != c.want || got.AuthKind != AuthAuto {
				t.Errorf("not persisted: %+v", got)
			}
		})
	}
}

func TestDetectUsesHostOverride(t *testing.T) {
	e := newEnv(t, nil)
	f := newAuthMCP(t, "none", false)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: f.URL, AuthKind: AuthAuto, Enabled: true, HostOverride: "localhost:8000"})
	up, _ := e.srv.Redetect(t.Context(), "u")
	if up.DetectedKind != "none" {
		t.Fatal(up.DetectedNote)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hosts) == 0 || f.hosts[0] != "localhost:8000" {
		t.Fatalf("probe ignored host override: %v", f.hosts)
	}
}

func TestAutoUsesDetectedKindAtRuntime(t *testing.T) {
	e := newEnv(t, nil)
	f := newAuthMCP(t, "bearer:UPSECRET", false)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: f.URL, AuthKind: AuthAuto, AuthValue: "UPSECRET", Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	h := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	// no stored detection yet: the first proxied request detects lazily and then uses bearer
	r := e.do("POST", "/mcp/u", h, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(b), "protocolVersion") {
		t.Fatalf("proxy through auto: %d %s", r.StatusCode, b)
	}
	up, _ := e.srv.Upstreams.Get("u")
	if up.DetectedKind != "bearer" {
		t.Fatalf("lazy detection not persisted: %+v", up)
	}
	if !strings.Contains(f.callLog(), "POST initialize auth=bearer:UPSECRET") {
		t.Fatalf("bearer not used at runtime:\n%s", f.callLog())
	}
	if up.EffectiveKind() != AuthBearer {
		t.Fatal("EffectiveKind")
	}
}

func TestExistingModesUntouchedAndManualOverride(t *testing.T) {
	e := newEnv(t, nil)
	f := newAuthMCP(t, "none", false)
	e.srv.Upstreams.Create(Upstream{Alias: "m", URL: f.URL, AuthKind: AuthBearer, AuthValue: "X", Enabled: true})
	up, _ := e.srv.Upstreams.Get("m")
	if up.AuthKind != AuthBearer || up.DetectedKind != "" || up.EffectiveKind() != AuthBearer {
		t.Fatalf("manual mode changed: %+v", up)
	}
	// re-detect on a manual upstream is informational only
	up, _ = e.srv.Redetect(t.Context(), "m")
	if up.DetectedKind != "none" {
		t.Fatalf("informational detection: %+v", up)
	}
	after, _ := e.srv.Upstreams.Get("m")
	if after.AuthKind != AuthBearer || after.EffectiveKind() != AuthBearer {
		t.Fatalf("detection must not change a manual mode: %+v", after)
	}
	// switching auto -> manual and back resets the stored detection
	e.srv.Upstreams.Create(Upstream{Alias: "a", URL: f.URL, AuthKind: AuthAuto, Enabled: true})
	e.srv.Redetect(t.Context(), "a")
	a, _ := e.srv.Upstreams.Get("a")
	a.AuthKind = AuthNone
	e.srv.Upstreams.Update(a, true)
	a.AuthKind = AuthAuto
	e.srv.Upstreams.Update(a, true)
	if a2, _ := e.srv.Upstreams.Get("a"); a2.DetectedKind != "" {
		t.Errorf("detection should reset on mode change: %+v", a2)
	}
	// an unchanged auto upstream keeps its detection across an edit
	e.srv.Redetect(t.Context(), "a")
	a, _ = e.srv.Upstreams.Get("a")
	e.srv.Upstreams.Update(a, true)
	if a2, _ := e.srv.Upstreams.Get("a"); a2.DetectedKind != "none" {
		t.Errorf("detection lost on no-op edit: %+v", a2)
	}
	// auto with failed/oauth detection sends no credentials
	if (Upstream{AuthKind: AuthAuto, DetectedKind: DetectedOAuth, AuthValue: "S"}).EffectiveKind() != AuthNone {
		t.Error("oauth detection must send nothing")
	}
}

func TestDecodeRPCBatchAndSSE(t *testing.T) {
	res, rerr, err := decodeRPC([]byte(`[{"jsonrpc":"2.0","method":"notifications/x"},{"jsonrpc":"2.0","id":1,"result":{"ok":true}}]`), 1)
	if err != nil || rerr != nil || !strings.Contains(string(res), "ok") {
		t.Fatalf("batch: %v %v %s", err, rerr, res)
	}
	res, _, err = readSSEReply(strings.NewReader("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"ping\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\n"+"data: \"result\":{\"a\":1}}\n\n"), 2)
	if err != nil || !strings.Contains(string(res), `"a":1`) {
		t.Fatalf("sse multi-line: %v %s", err, res)
	}
	if _, _, err = readSSEReply(strings.NewReader("data: nope\n\n"), 1); err == nil {
		t.Fatal("garbage sse must fail")
	}
}
