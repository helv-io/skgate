package mcp

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Every skgate endpoint answers identically with and without a trailing slash, and never with a
// redirect (a client that cannot follow one would just fail).
func TestInboundTrailingSlashEquivalence(t *testing.T) {
	e := newEnv(t, nil)
	up := newAggUp(t, "none", "t1")
	e.srv.Upstreams.Create(Upstream{Alias: "a", URL: up.URL, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
	key, _, _ := e.keys.Create("t")
	auth := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}
	read := func(r *http.Response) (int, string) {
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.Header.Get("Location") != "" || (r.StatusCode >= 300 && r.StatusCode < 400) {
			t.Errorf("redirect to the client: %d Location=%q", r.StatusCode, r.Header.Get("Location"))
		}
		return r.StatusCode, string(b)
	}
	for _, pair := range [][2]string{{"/mcp", "/mcp/"}, {"/mcp/a", "/mcp/a/"}} {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
		if pair[0] == "/mcp/a" {
			body = `{"jsonrpc":"2.0","id":1,"method":"initialize"}` // a transparent proxy: initialize needs no session
		}
		s1, b1 := read(e.do("POST", pair[0], auth, body))
		s2, b2 := read(e.do("POST", pair[1], auth, body))
		if s1 != 200 || s1 != s2 || (pair[0] == "/mcp" && b1 != b2) {
			t.Errorf("%v differ: %d %s | %d %s", pair, s1, b1, s2, b2)
		}
	}
	// unauthenticated answers are the same 401 challenge on both spellings
	for _, p := range []string{"/mcp", "/mcp/", "/mcp/a", "/mcp/a/", "/sse", "/sse/", "/sse/a", "/sse/a/"} {
		if s, _ := read(e.do("GET", p, nil, "")); s != 401 {
			t.Errorf("GET %s unauthenticated: %d", p, s)
		}
	}
	// OAuth and discovery endpoints
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/",
		"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource/mcp/",
		"/.well-known/oauth-protected-resource/mcp/a", "/.well-known/oauth-protected-resource/mcp/a/"} {
		if s, b := read(e.do("GET", p, nil, "")); s != 200 || !strings.Contains(b, "{") {
			t.Errorf("GET %s: %d %s", p, s, b)
		}
	}
	// the protected resource document is the same for both spellings
	_, d1 := read(e.do("GET", "/.well-known/oauth-protected-resource/mcp/a", nil, ""))
	_, d2 := read(e.do("GET", "/.well-known/oauth-protected-resource/mcp/a/", nil, ""))
	if d1 != d2 {
		t.Errorf("PRM differs: %s | %s", d1, d2)
	}
	for _, pair := range [][2]string{{"/register", "/register/"}, {"/token", "/token/"}} {
		s1, _ := read(e.do("POST", pair[0], map[string]string{"Content-Type": "application/json"}, `{}`))
		s2, _ := read(e.do("POST", pair[1], map[string]string{"Content-Type": "application/json"}, `{}`))
		if s1 != s2 || s1 == 404 {
			t.Errorf("%v: %d vs %d", pair, s1, s2)
		}
	}
	reg := e.register(hostedRedirect, "none")
	_, challenge := pkce()
	for _, base := range []string{"/authorize", "/authorize/"} {
		id := reg["client_id"].(string)
		q := "?response_type=code&client_id=" + id + "&redirect_uri=" + hostedRedirect + "&state=s&code_challenge=" + challenge + "&code_challenge_method=S256"
		r := e.do("GET", base+q, nil, "")
		r.Body.Close()
		if r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), hostedRedirect) {
			t.Errorf("%s: %d %s", base, r.StatusCode, r.Header.Get("Location"))
		}
	}
}
