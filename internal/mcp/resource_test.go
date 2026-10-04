package mcp

import "testing"

// RFC 8707: a token request may narrow the resource a code or refresh token was issued for, never widen it.
func TestTokenEndpointNeverWidensTheResource(t *testing.T) {
	e := newEnv(t, nil)
	d := newDocServer(t)
	e.trust(d)
	narrow := e.srv.Issuer() + "/mcp/a"
	exchange := func(resourceAtToken string) map[string]any {
		t.Helper()
		verifier, challenge := pkce()
		code, _, st := e.authorizeCode(d.id, hostedRedirect, challenge, "&scope=mcp&resource="+narrow)
		if st != 302 || code == "" {
			t.Fatalf("authorize: %d", st)
		}
		args := []string{"grant_type", "authorization_code", "client_id", d.id, "code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect}
		if resourceAtToken != "" {
			args = append(args, "resource", resourceAtToken)
		}
		r := e.do("POST", "/token", formHdr, form(args...))
		m := readJSON(t, r)
		m["_status"] = float64(r.StatusCode)
		return m
	}
	if m := exchange(e.srv.Issuer()); m["_status"] != float64(400) || m["error"] != "invalid_target" {
		t.Errorf("a code for %s was exchanged for the whole server: %v", narrow, m)
	}
	m := exchange(narrow + "/deeper")
	if m["_status"] != float64(200) {
		t.Fatalf("narrowing was refused: %v", m)
	}
	m = exchange("")
	refresh, _ := m["refresh_token"].(string)
	if refresh == "" {
		t.Fatalf("no refresh token: %v", m)
	}
	r := e.do("POST", "/token", formHdr, form("grant_type", "refresh_token", "client_id", d.id, "refresh_token", refresh, "resource", e.srv.Issuer()))
	if r.StatusCode != 400 {
		t.Errorf("a refresh token for %s was widened to the whole server: %d", narrow, r.StatusCode)
	}
	m = exchange("")
	refresh, _ = m["refresh_token"].(string)
	r = e.do("POST", "/token", formHdr, form("grant_type", "refresh_token", "client_id", d.id, "refresh_token", refresh, "resource", narrow))
	if r.StatusCode != 200 {
		t.Errorf("refresh for the same resource: %d", r.StatusCode)
	}
}
