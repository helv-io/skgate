package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestClientLastUsedTracking(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	c, _ := e.srv.Clients.Get(id)
	if !c.LastUsed.IsZero() {
		t.Fatalf("registering is not a use: %v", c.LastUsed)
	}
	verifier, challenge := pkce()
	code, _, _ := e.authorizeCode(id, hostedRedirect, challenge, "")
	if code == "" {
		t.Fatal("authorize failed")
	}
	c, _ = e.srv.Clients.Get(id)
	if c.LastUsed.IsZero() || time.Since(c.LastUsed) > time.Minute {
		t.Fatalf("/authorize must record last use: %v", c.LastUsed)
	}
	// rewind, then /token touches it again
	e.srv.Clients.db.Exec(`UPDATE oauth_clients SET last_used_at=1000 WHERE client_id=?`, id)
	resp := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "code", code, "code_verifier", verifier, "client_id", id, "redirect_uri", hostedRedirect))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	c, _ = e.srv.Clients.Get(id)
	if time.Since(c.LastUsed) > time.Minute {
		t.Fatalf("/token must record last use: %v", c.LastUsed)
	}
	// failed client authentication and unknown clients do not count as use
	sec := e.register(hostedRedirect, "client_secret_post")
	sid := sec["client_id"].(string)
	resp = e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "code", "x", "client_id", sid, "client_secret", strings.Repeat("z", 40)))
	resp.Body.Close()
	if c, _ := e.srv.Clients.Get(sid); !c.LastUsed.IsZero() {
		t.Fatal("bad secret must not count as use")
	}
	e.srv.Clients.Touch("skc-missing") // no row, no panic
	list, _ := e.srv.Clients.List()
	for _, l := range list {
		if l.ID == sid && !l.LastUsed.IsZero() {
			t.Fatal("list reports a use that never happened")
		}
	}
}

func TestUpstreamSetFlagTouchesOnlyThatFlag(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "f1", URL: "http://x.example/mcp", AuthKind: AuthBearer, AuthValue: "tok-12345678", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	u, err := e.srv.Upstreams.SetFlag("f1", FlagInclude, true)
	if err != nil || !u.IncludeInMCP || !u.Enabled || u.AuthValue != "tok-12345678" {
		t.Fatalf("%+v %v", u, err)
	}
	if u, _ = e.srv.Upstreams.SetFlag("f1", FlagEnabled, false); u.Enabled || !u.IncludeInMCP {
		t.Fatalf("%+v", u)
	}
	if inc, _ := e.srv.Upstreams.Included(); len(inc) != 0 {
		t.Fatal("a disabled upstream is not served by /mcp even when included")
	}
	if _, err := e.srv.Upstreams.SetFlag("nope", FlagEnabled, true); err == nil {
		t.Fatal("unknown alias must error")
	}
	if _, err := e.srv.Upstreams.SetFlag("f1", "url", true); err == nil {
		t.Fatal("unknown flag must error")
	}
}

func TestUpstreamCredentialsEncryptedAtRest(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "enc", URL: "http://x.example/mcp", AuthKind: AuthBearer, AuthValue: "tok-very-secret", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	var raw string
	e.db.QueryRow(`SELECT auth_value FROM upstreams WHERE alias='enc'`).Scan(&raw)
	if raw == "tok-very-secret" || strings.Contains(raw, "very-secret") || !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("stored plaintext: %q", raw)
	}
	if u, _ := e.srv.Upstreams.Get("enc"); u.AuthValue != "tok-very-secret" || u.SecretErr {
		t.Fatalf("read back: %+v", u)
	}
	// edit keeping the secret (empty value) must not lose or double-encrypt it
	u, _ := e.srv.Upstreams.Get("enc")
	u.AuthValue = ""
	if err := e.srv.Upstreams.Update(u, true); err != nil {
		t.Fatal(err)
	}
	if u, _ = e.srv.Upstreams.Get("enc"); u.AuthValue != "tok-very-secret" {
		t.Fatalf("after keep: %+v", u)
	}
	// unreadable secret is flagged, not returned
	e.db.Exec(`UPDATE upstreams SET auth_value='enc:v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' WHERE alias='enc'`)
	if u, _ = e.srv.Upstreams.Get("enc"); !u.SecretErr || u.AuthValue != "" {
		t.Fatalf("undecryptable: %+v", u)
	}
}
