package mcp

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
)

// lineFor returns the last log line containing all of the fragments. The line is written after
// the handler returns, which can be just after the client saw the response, so it polls briefly.
func (e *env) lineFor(frags ...string) string {
	e.t.Helper()
	for try := 0; ; try++ {
		lines := strings.Split(e.logs.String(), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			ok := true
			for _, f := range frags {
				if !strings.Contains(lines[i], f) {
					ok = false
					break
				}
			}
			if ok {
				return lines[i]
			}
		}
		if try >= 100 {
			e.t.Fatalf("no log line with %v in:\n%s", frags, e.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLoggingRejectionReasons(t *testing.T) {
	e := newEnv(t, nil)
	up := newFakeMCP(t)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: up.URL, AuthKind: AuthNone, Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "dead", URL: "http://127.0.0.1:1/mcp", AuthKind: AuthNone, Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "needs", URL: up.URL, AuthKind: AuthNone, Enabled: true})
	key, _, _ := e.keys.Create("t")
	bearer := map[string]string{"Authorization": "Bearer " + key, "User-Agent": "UA-Test/9", "Origin": "https://app.example", "Content-Type": "application/json"}

	// missing token
	e.do("POST", "/mcp/u", map[string]string{"User-Agent": "UA-Test/9", "Origin": "https://app.example"}, `{}`)
	l := e.lineFor("path=/mcp/u", "status=401", "reason=")
	for _, want := range []string{"missing token", "ua=UA-Test/9", "origin=https://app.example", "method=POST", "dur="} {
		if !strings.Contains(l, want) {
			t.Errorf("missing-token line lacks %q: %s", want, l)
		}
	}
	// invalid token: never logs the token itself, only its last 4 chars masked
	bad := "sk-" + strings.Repeat("z", 48)
	e.do("POST", "/mcp/u", map[string]string{"Authorization": "Bearer " + bad}, `{}`)
	l = e.lineFor("status=401", "invalid token")
	if strings.Contains(l, bad) || !strings.Contains(l, "************zzzz") {
		t.Errorf("invalid token line: %s", l)
	}
	e.do("POST", "/mcp/u", map[string]string{"Authorization": "Bearer skat_NOTREALTOKENVALUE"}, `{}`)
	l = e.lineFor("status=401", "OAuth access token is unknown")
	if strings.Contains(l, "NOTREALTOKENVALUE") {
		t.Errorf("token leaked: %s", l)
	}
	// success line carries upstream info and no reason
	r := e.do("POST", "/mcp/u", bearer, `{"method":"initialize"}`)
	r.Body.Close()
	l = e.lineFor("path=/mcp/u", "status=200", "upstream=u")
	if strings.Contains(l, "reason=") || !strings.Contains(l, "upstream_status=200") || !strings.Contains(l, "upstream_auth=none") {
		t.Errorf("ok line: %s", l)
	}
	// unknown alias
	e.do("POST", "/mcp/nope", bearer, `{}`)
	e.lineFor("path=/mcp/nope", "status=404", "unknown upstream")
	// upstream unreachable
	e.do("POST", "/mcp/dead", bearer, `{}`)
	l = e.lineFor("path=/mcp/dead", "status=502", "upstream unreachable")
	if strings.Contains(l, "127.0.0.1:1/mcp") && strings.Contains(l, "http://") {
		t.Errorf("full upstream URL logged: %s", l)
	}
	// upstream 401 (fake answers 401 for body "needs-auth")
	e.do("POST", "/mcp/needs", bearer, `needs-auth`)
	l = e.lineFor("path=/mcp/needs", "status=502", "upstream error", "HTTP 401")
	if !strings.Contains(l, "advertises OAuth") || !strings.Contains(l, "upstream_status=401") {
		t.Errorf("upstream 401 line: %s", l)
	}
}

func TestLoggingOAuthReasons(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	verifier, challenge := pkce()

	// bad redirect (plain http off loopback) at /register
	e.do("POST", "/register", map[string]string{"Content-Type": "application/json"}, `{"redirect_uris":["http://evil.example/cb"]}`)
	l := e.lineFor("path=/register", "status=400", "bad redirect origin")
	if !strings.Contains(l, "redirect_host=evil.example") {
		t.Errorf("register: %s", l)
	}
	// unknown client
	e.authorizeCode("skc-nope", hostedRedirect, challenge, "")
	e.lineFor("path=/authorize", "status=400", "unknown client", "client_id=skc-nope")
	// unregistered redirect
	e.authorizeCode(id, "https://hosted.example/other", challenge, "")
	e.lineFor("path=/authorize", "status=400", "does not exactly match", "redirect_host=hosted.example")
	// missing PKCE
	e.authorizeCode(id, hostedRedirect, "", "")
	e.lineFor("path=/authorize", "status=302", "PKCE required")
	// success + PKCE mismatch at /token
	code, _, _ := e.authorizeCode(id, hostedRedirect, challenge, "")
	l = e.lineFor("path=/authorize", "status=302", "client_id="+id)
	if strings.Contains(l, code) || strings.Contains(l, "reason=") {
		t.Errorf("authorize success line: %s", l)
	}
	e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "code", code, "code_verifier", strings.Repeat("x", 50), "client_id", id))
	l = e.lineFor("path=/token", "status=400", "PKCE mismatch")
	if strings.Contains(l, code) || !strings.Contains(l, "client_id="+id) {
		t.Errorf("token line: %s", l)
	}
	// unknown client at /token
	e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "code", "c", "code_verifier", verifier, "client_id", "skc-nope"))
	e.lineFor("path=/token", "status=401", "unknown client")
	// well-known is logged
	e.do("GET", "/.well-known/oauth-authorization-server", nil, "")
	e.lineFor("path=/.well-known/oauth-authorization-server", "status=200")
	// healthz and admin are not
	e.do("GET", "/healthz", nil, "")
	if strings.Contains(e.logs.String(), "/healthz") {
		t.Error("healthz must not be logged")
	}
}

func TestLoggingNeverContainsSecretsAtDebug(t *testing.T) {
	e2 := newEnv(t, func(c *config.Config) { c.LogLevel = "debug" })
	reg := e2.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	_, challenge := pkce()
	q := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {hostedRedirect}, "state": {"STATE-SECRET-1"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	resp := e2.do("GET", "/authorize?"+q.Encode()+"&access_token=AT-LEAK&id_token=IDT-LEAK", map[string]string{"Cookie": "skgate_session=COOKIE-LEAK", "Authorization": "Bearer HDR-LEAK"}, "")
	loc := resp.Header.Get("Location")
	code := ""
	if u, err := url.Parse(loc); err == nil {
		code = u.Query().Get("code")
	}
	out := e2.logs.String()
	if !strings.Contains(out, "query=") || !strings.Contains(out, "credentials=") {
		t.Fatalf("debug detail missing: %s", out)
	}
	for _, leak := range []string{"STATE-SECRET-1", "AT-LEAK", "IDT-LEAK", "COOKIE-LEAK", "HDR-LEAK", challenge} {
		if strings.Contains(out, leak) {
			t.Errorf("debug log leaks %q:\n%s", leak, out)
		}
	}
	if code != "" && strings.Contains(out, code) {
		t.Errorf("debug log leaks the authorization code:\n%s", out)
	}
}
