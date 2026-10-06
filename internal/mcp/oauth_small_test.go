package mcp

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/httputil"
)

func issueTokens(t *testing.T, e *env, resource string) (access, refresh string) {
	t.Helper()
	reg := e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	verifier, challenge := pkce()
	extra := "&scope=mcp"
	if resource != "" {
		extra += "&resource=" + url.QueryEscape(resource)
	}
	code, _, st := e.authorizeCode(id, hostedRedirect, challenge, extra)
	if st != 302 || code == "" {
		t.Fatalf("authorize: %d", st)
	}
	args := []string{"grant_type", "authorization_code", "client_id", id, "code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect}
	if resource != "" {
		args = append(args, "resource", resource)
	}
	resp := e.do("POST", "/token", formHdr, form(args...))
	m := readJSON(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("token: %d %v", resp.StatusCode, m)
	}
	access, _ = m["access_token"].(string)
	refresh, _ = m["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("token response: %v", m)
	}
	return access, refresh
}

func refreshGrant(e *env, id, refresh, resource string) (int, map[string]any) {
	args := []string{"grant_type", "refresh_token", "client_id", id, "refresh_token", refresh}
	if resource != "" {
		args = append(args, "resource", resource)
	}
	resp := e.do("POST", "/token", formHdr, form(args...))
	return resp.StatusCode, readJSON(e.t, resp)
}

// /sse advertises its own protected-resource document. A token for that resource is accepted.
// A token issued for /mcp is not.
func TestSSEOwnProtectedResource(t *testing.T) {
	e := newEnv(t, nil)
	for path, meta := range map[string]string{
		"/sse":     e.ts.URL + "/.well-known/oauth-protected-resource/sse",
		"/sse/foo": e.ts.URL + "/.well-known/oauth-protected-resource/sse/foo",
	} {
		resp := e.do("GET", path, nil, "")
		if resp.StatusCode != 401 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		want := `Bearer resource_metadata="` + meta + `"`
		if got := resp.Header.Get("WWW-Authenticate"); got != want {
			t.Errorf("%s: WWW-Authenticate=%q want %q", path, got, want)
		}
		resp.Body.Close()
	}
	msg := e.do("POST", "/messages", nil, `{}`)
	if msg.StatusCode != 401 || !strings.Contains(msg.Header.Get("WWW-Authenticate"), `/.well-known/oauth-protected-resource/sse"`) || strings.Contains(msg.Header.Get("WWW-Authenticate"), "/mcp") {
		t.Fatalf("/messages 401: %d %q", msg.StatusCode, msg.Header.Get("WWW-Authenticate"))
	}
	msg.Body.Close()
	doc := readJSON(t, e.do("GET", "/.well-known/oauth-protected-resource/sse", nil, ""))
	if doc["resource"] != e.ts.URL+"/sse" {
		t.Fatalf("sse document: %v", doc["resource"])
	}
	alias := readJSON(t, e.do("GET", "/.well-known/oauth-protected-resource/sse/foo", nil, ""))
	if alias["resource"] != e.ts.URL+"/sse/foo" {
		t.Fatalf("sse alias document: %v", alias["resource"])
	}

	sse := e.ts.URL + "/sse"
	mcp := e.ts.URL + "/mcp"
	sseAccess, _ := issueTokens(t, e, sse)
	mcpAccess, _ := issueTokens(t, e, mcp)
	bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
	// unknown alias: auth succeeded, so the answer is 404 rather than 401
	if r := e.do("GET", "/sse/foo", bearer(sseAccess), ""); r.StatusCode != 404 {
		t.Fatalf("token for /sse on /sse/foo: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := e.do("GET", "/sse/foo", bearer(mcpAccess), ""); r.StatusCode != 401 {
		t.Fatalf("token for /mcp on /sse/foo: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := e.do("POST", "/messages", bearer(sseAccess), `{}`); r.StatusCode != 404 {
		t.Fatalf("token for /sse on /messages: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := e.do("POST", "/mcp/foo", map[string]string{"Authorization": "Bearer " + sseAccess, "Content-Type": "application/json"}, `{}`); r.StatusCode != 401 {
		t.Fatalf("token for /sse on /mcp/foo: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
}

// The refresh token from a rotation keeps working after the previous one is retried once.
// Once the grace window has passed, the previous token stops.
func TestRefreshGrace(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	verifier, challenge := pkce()
	code, _, st := e.authorizeCode(id, hostedRedirect, challenge, "&scope=mcp")
	if st != 302 || code == "" {
		t.Fatalf("authorize: %d", st)
	}
	resp := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "client_id", id, "code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect))
	issued := readJSON(t, resp)
	refresh, _ := issued["refresh_token"].(string)
	if resp.StatusCode != 200 || refresh == "" {
		t.Fatalf("token: %d %v", resp.StatusCode, issued)
	}

	st, rotated := refreshGrant(e, id, refresh, "")
	if st != 200 {
		t.Fatalf("rotate: %d %v", st, rotated)
	}
	next, _ := rotated["refresh_token"].(string)
	st, _ = refreshGrant(e, id, refresh, "")
	if st != 200 {
		t.Fatalf("grace retry: %d", st)
	}
	st, again := refreshGrant(e, id, next, "")
	if st != 200 {
		t.Fatalf("the new refresh token was invalidated by the retry: %d", st)
	}
	kept, _ := again["refresh_token"].(string)
	_, _ = e.db.Exec(`UPDATE oauth_tokens SET expires_at=? WHERE hash=?`, time.Now().Add(-time.Second).Unix(), httputil.SHA256Hex(next))
	if st, _ = refreshGrant(e, id, next, ""); st != 400 {
		t.Fatalf("previous token after the grace window: %d", st)
	}
	if st, _ = refreshGrant(e, id, kept, ""); st != 200 {
		t.Fatalf("closing the window on the previous token signed the client out: %d", st)
	}
}

func TestRevokeEndpoint(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	verifier, challenge := pkce()
	code, _, st := e.authorizeCode(id, hostedRedirect, challenge, "&scope=mcp")
	if st != 302 || code == "" {
		t.Fatalf("authorize: %d", st)
	}
	resp := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "client_id", id, "code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect))
	m := readJSON(t, resp)
	access, _ := m["access_token"].(string)
	refresh, _ := m["refresh_token"].(string)

	if r := e.do("GET", "/revoke", nil, ""); r.StatusCode != 405 {
		t.Fatalf("GET /revoke: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := e.do("POST", "/revoke", formHdr, form("client_id", id)); r.StatusCode != 400 {
		t.Fatalf("missing token: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := e.do("POST", "/revoke", formHdr, form("token", access)); r.StatusCode != 400 {
		t.Fatalf("missing client: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	// someone else's client must not revoke this token
	other := e.register(hostedRedirect, "none")
	if r := e.do("POST", "/revoke", formHdr, form("client_id", other["client_id"].(string), "token", access)); r.StatusCode != 200 {
		t.Fatalf("other client: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := e.do("POST", "/mcp", map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"}, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`); r.StatusCode == 401 {
		t.Fatal("another client revoked the token")
	} else {
		r.Body.Close()
	}
	if r := e.do("POST", "/revoke", formHdr, form("client_id", id, "token", access, "token_type_hint", "access_token")); r.StatusCode != 200 {
		t.Fatalf("revoke access: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := e.do("POST", "/mcp", map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"}, `{}`); r.StatusCode != 401 {
		t.Fatalf("revoked access token: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if st, _ := refreshGrant(e, id, refresh, ""); st != 200 {
		t.Fatalf("refresh still works after the access token was revoked: %d", st)
	}
	// refreshGrant rotated `refresh` and left it in the grace window. Revoking that token ends the retry.
	if r := e.do("POST", "/revoke", formHdr, form("client_id", id, "token", refresh, "token_type_hint", "refresh_token")); r.StatusCode != 200 {
		t.Fatalf("revoke refresh: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if st, _ := refreshGrant(e, id, refresh, ""); st != 400 {
		t.Fatalf("revoked refresh token: %d", st)
	}
	if r := e.do("POST", "/revoke", formHdr, form("client_id", id, "token", "not-a-token")); r.StatusCode != 200 {
		t.Fatalf("unknown token: %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
}
