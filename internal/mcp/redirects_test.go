package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// redirectingMCP answers POST /mcp/ with a redirect (like a server that normalizes the trailing
// slash) and serves the real endpoint at /mcp.
type redirectingMCP struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	got    []string // "<method> <path> auth=<Authorization> body=<body>"
	hosts  []string
}

func newRedirectingMCP(t *testing.T, status int, loc func(r *http.Request) string) *redirectingMCP {
	f := &redirectingMCP{status: status}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.got = append(f.got, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization")+" body="+string(b))
		f.hosts = append(f.hosts, r.Host)
		f.mu.Unlock()
		if r.URL.Path == "/mcp/" || strings.HasPrefix(r.URL.Path, "/away") {
			w.Header().Set("Location", loc(r))
			w.WriteHeader(f.status)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/loop") {
			w.Header().Set("Location", r.URL.Path)
			w.WriteHeader(f.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func TestUpstreamRedirectFollowedServerSide(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308} {
		e := newEnv(t, nil)
		// The Location names the internal host, exactly like the prod 307 that leaked it.
		f := newRedirectingMCP(t, code, func(r *http.Request) string { return "http://" + r.Host + "/mcp" })
		e.srv.Upstreams.Create(Upstream{Alias: "bb", URL: f.URL + "/mcp/", AuthKind: AuthBearer, AuthValue: "UPSECRET", Enabled: true, IncludeInMCP: true})
		key, _, _ := e.keys.Create("t")
		h := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
		r := e.do("POST", "/mcp/bb", h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || r.Header.Get("Location") != "" || !strings.Contains(string(b), `"ok":true`) {
			t.Fatalf("%d: client got %d Location=%q body=%s", code, r.StatusCode, r.Header.Get("Location"), b)
		}
		f.mu.Lock()
		got := strings.Join(f.got, "\n")
		f.mu.Unlock()
		want := []string{"POST /mcp/ auth=Bearer UPSECRET", "POST /mcp auth=Bearer UPSECRET body={\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}"}
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%d: upstream calls lack %q:\n%s", code, w, got)
			}
		}
	}
}

func TestUpstreamRedirectToOtherHostIsNotFollowed(t *testing.T) {
	e := newEnv(t, nil)
	other := newRedirectingMCP(t, 200, nil)
	f := newRedirectingMCP(t, 307, func(*http.Request) string { return other.URL + "/mcp" })
	e.srv.Upstreams.Create(Upstream{Alias: "bb", URL: f.URL + "/away", AuthKind: AuthBearer, AuthValue: "UPSECRET", Enabled: true})
	key, _, _ := e.keys.Create("t")
	r := e.do("POST", "/mcp/bb", map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}, `{}`)
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 502 || r.Header.Get("Location") != "" || strings.Contains(string(b), other.URL) || strings.Contains(string(b), "127.0.0.1") {
		t.Fatalf("cross-host redirect: %d Location=%q %s", r.StatusCode, r.Header.Get("Location"), b)
	}
	other.mu.Lock()
	defer other.mu.Unlock()
	if len(other.got) != 0 {
		t.Fatalf("credentials were sent to another host: %v", other.got)
	}
	if !strings.Contains(e.logs.String(), "another host") {
		t.Errorf("reason not logged: %s", e.logs.String())
	}
}

func TestUpstreamRedirectLoopStops(t *testing.T) {
	e := newEnv(t, nil)
	f := newRedirectingMCP(t, 307, nil)
	e.srv.Upstreams.Create(Upstream{Alias: "bb", URL: f.URL + "/loop", AuthKind: AuthNone, Enabled: true})
	key, _, _ := e.keys.Create("t")
	r := e.do("POST", "/mcp/bb", map[string]string{"Authorization": "Bearer " + key}, `{}`)
	r.Body.Close()
	f.mu.Lock()
	n := len(f.got)
	f.mu.Unlock()
	if r.StatusCode != 502 || n != 2*(maxUpstreamHops+1) {
		t.Fatalf("loop: status %d after %d requests", r.StatusCode, n)
	}
}

func TestRewriteLocation(t *testing.T) {
	up := Upstream{Alias: "bb", URL: "http://notes-mcp:8080/mcp/"}
	h := http.Header{"Location": {"http://notes-mcp:8080/mcp"}}
	rewriteLocation(h, up, "https://gw.example")
	if h.Get("Location") != "" {
		t.Errorf("absolute upstream Location kept: %q", h.Get("Location"))
	}
	h = http.Header{"Location": {"/mcp"}}
	rewriteLocation(h, up, "https://gw.example/")
	if h.Get("Location") != "https://gw.example/mcp/bb" {
		t.Errorf("relative Location: %q", h.Get("Location"))
	}
}
