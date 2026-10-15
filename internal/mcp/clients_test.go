package mcp

import (
	"net/http"
	"net/http/httptest"
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
	if raw == "tok-very-secret" || strings.Contains(raw, "very-secret") || !strings.HasPrefix(raw, "enc:v2:") {
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

// Clients nobody used for a while: last used (or, never used, created) before the cutoff. Their tokens go with them.
func TestDeleteUnusedClients(t *testing.T) {
	e := newEnv(t, nil)
	db, now := e.srv.Clients.db, time.Now()
	mk := func(id string, created, used time.Time) {
		c, err := e.srv.Clients.Create(Client{ID: id, Name: id, RedirectURIs: []string{hostedRedirect}, AuthMethod: "none", Source: "dcr"}, "")
		if err != nil {
			t.Fatal(err)
		}
		_ = c
		db.Exec(`UPDATE oauth_clients SET created_at=? WHERE client_id=?`, created.Unix(), id)
		if !used.IsZero() {
			db.Exec(`UPDATE oauth_clients SET last_used_at=? WHERE client_id=?`, used.Unix(), id)
		}
	}
	old, recent := now.Add(-45*24*time.Hour), now.Add(-2*24*time.Hour)
	mk("never-old", old, time.Time{})    // never used, created long ago: unused
	mk("never-new", recent, time.Time{}) // never used but just created: kept
	mk("used-old", old, old)             // last used long ago: unused
	mk("used-recently", old, recent)     // created long ago but used lately: kept
	db.Exec(`INSERT INTO oauth_tokens(hash,kind,client_id,expires_at) VALUES('h1','access','used-old',?)`, now.Add(time.Hour).Unix())
	cutoff := now.Add(-30 * 24 * time.Hour)
	if n := e.srv.Clients.CountUnused(cutoff); n != 2 {
		t.Fatalf("CountUnused = %d, want 2", n)
	}
	n, err := e.srv.Clients.DeleteUnused(cutoff)
	if err != nil || n != 2 {
		t.Fatalf("DeleteUnused = %d, %v", n, err)
	}
	for id, want := range map[string]bool{"never-old": false, "used-old": false, "never-new": true, "used-recently": true} {
		if _, ok := e.srv.Clients.Get(id); ok != want {
			t.Errorf("%s present = %v, want %v", id, ok, want)
		}
	}
	var tokens int
	db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE client_id='used-old'`).Scan(&tokens)
	if tokens != 0 {
		t.Errorf("the tokens of a deleted client are deleted too: %d", tokens)
	}
}

func TestSavedPublicIPOverHTTPGivesAClearError(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "pub", URL: "http://8.8.8.8/mcp", Enabled: true}); err == nil {
		t.Fatal("a public IP over http must be refused on save")
	}
	// a row saved by an older release
	e.db.Exec(`INSERT INTO upstreams(alias,url,created_at) VALUES('old','http://8.8.8.8/mcp',0)`)
	up, ok := e.srv.Upstreams.Get("old")
	if !ok {
		t.Fatal("row missing")
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/old", nil)
	_, err := e.srv.forward(req, up, http.MethodPost, []byte("{}"), nil)
	if err == nil || !strings.Contains(err.Error(), "use https") || !strings.Contains(err.Error(), "old") {
		t.Fatalf("want a clear https error, got %v", err)
	}
	if tr := e.srv.testUpstream(req.Context(), up); !strings.Contains(tr.Error, "use https") {
		t.Fatalf("test: %+v", tr)
	}
}

func TestPassthroughUpstreamsAreNotOnAggregatedMCP(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "pt", URL: "http://pt:8000/mcp", AuthKind: AuthPassthrough, Enabled: true, IncludeInMCP: true}); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.srv.Upstreams.Get("pt"); u.IncludeInMCP {
		t.Fatal("saving a passthrough upstream must clear In /mcp")
	}
	if _, err := e.srv.Upstreams.SetFlag("pt", FlagInclude, true); err != ErrPassthroughInMCP {
		t.Fatalf("toggle: %v", err)
	}
	// a row from an older release that still has the flag
	e.db.Exec(`UPDATE upstreams SET include_in_mcp=1 WHERE alias='pt'`)
	if inc, _ := e.srv.Upstreams.Included(); len(inc) != 0 {
		t.Fatalf("included: %+v", inc)
	}
}

func TestMCPRateLimitAnswers429(t *testing.T) {
	e := newEnv(t, nil)
	full, k, _ := e.keys.Create("m")
	if err := e.keys.SetMCPRate(k.ID, 1); err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	hdr := map[string]string{"Authorization": "Bearer " + full, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	if r := e.do("POST", "/mcp", hdr, body); r.StatusCode == http.StatusTooManyRequests || r.StatusCode == http.StatusUnauthorized {
		t.Fatalf("first request: %d", r.StatusCode)
	}
	r := e.do("POST", "/mcp", hdr, body)
	if r.StatusCode != http.StatusTooManyRequests || r.Header.Get("Retry-After") == "" {
		t.Fatalf("second request: %d", r.StatusCode)
	}
	// /v1 is not counted against the MCP limit and vice versa: another key is untouched
	other, _, _ := e.keys.Create("o")
	hdr["Authorization"] = "Bearer " + other
	if r := e.do("POST", "/mcp", hdr, body); r.StatusCode == http.StatusTooManyRequests {
		t.Fatal("another key must not be limited")
	}
}

// Every outbound MCP request follows the address rule, including the /mcp aggregator and the port probe, not only
// /mcp/{alias} and the Test page (re-audit SEC-05): a saved plain-http public address is not called.
func TestRPCPostRefusesPlainHTTPOnTheInternet(t *testing.T) {
	e := newEnv(t, nil)
	for _, up := range []Upstream{{Alias: "old", URL: "http://8.8.8.8/mcp"}, {URL: "http://mcp.example.com/mcp"}} {
		rep, err := e.srv.rpcPost(t.Context(), up, initBody(), 1, "", "", time.Second)
		if rep != nil || err == nil || !strings.Contains(err.Error(), "use https") {
			t.Fatalf("%s: want a clear https error and no request, got %v %v", up.URL, rep, err)
		}
		if up.Alias != "" && !strings.Contains(err.Error(), up.Alias) {
			t.Errorf("the error names the upstream: %v", err)
		}
	}
	// a LAN or loopback address over http still works
	f := newAggUp(t, "none", "t")
	defer f.Close()
	rep, err := e.srv.rpcPost(t.Context(), Upstream{Alias: "lan", URL: f.URL, AuthKind: AuthNone}, initBody(), 1, "", "", 5*time.Second)
	if err != nil || rep == nil || rep.Status != 200 {
		t.Fatalf("http on a private address must still work: %v %+v", err, rep)
	}
}
