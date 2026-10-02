package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/oidctest"
)

const clientRedirect = "https://grok.com/cb"

func pkcePair() (verifier, challenge string) {
	verifier = strings.Repeat("v", 20) + strings.Repeat("Ab1-", 10)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func registerClient(t *testing.T, ts *httptest.Server, redirect string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"client_name": "client", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"})
	resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	id, _ := m["client_id"].(string)
	if resp.StatusCode != 201 || id == "" {
		t.Fatalf("register: %d %v", resp.StatusCode, m)
	}
	return id
}

func authorizePath(clientID, redirect, challenge string) string {
	return "/authorize?" + url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"state": {"st-xyz"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"mcp"}}.Encode()
}

func idpHits(idp *oidctest.Provider, path string) int {
	n := 0
	for _, p := range idp.Paths() {
		if p == path {
			n++
		}
	}
	return n
}

// followToIdP performs /authorize, the OIDC login hop, and the IdP authorization; it returns the
// IdP callback URL. It fails the test if /authorize did not bounce to login.
func followToIdP(t *testing.T, br *browser, idp *oidctest.Provider, authPath string) string {
	t.Helper()
	r, _ := br.get(authPath)
	loc := r.Header.Get("Location")
	if r.StatusCode != 302 || !strings.HasPrefix(loc, "/admin/oidc/login?next=") {
		t.Fatalf("/authorize without session should redirect to OIDC login, got %d %q", r.StatusCode, loc)
	}
	r, _ = br.get(loc)
	if r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), idp.URL+"/x/authz?") {
		t.Fatalf("oidc login should redirect to the IdP: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	return idp.Authorize(t, r.Header.Get("Location"))
}

func TestMCPOAuthEndToEndWithOIDC(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("client credentials leaked to the upstream: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"echo":`+string(b)+`}}`)
	}))
	defer up.Close()
	a, ts, idp := newApp(t, nil)
	if err := a.MCP.Upstreams.Create(mcp.Upstream{Alias: "tools", URL: up.URL, AuthKind: mcp.AuthNone, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	clientID := registerClient(t, ts, clientRedirect)
	verifier, challenge := pkcePair()
	authPath := authorizePath(clientID, clientRedirect, challenge)
	br := newBrowser(t, ts)

	// 1. no session: bounce through OIDC login and the IdP
	cbURL := followToIdP(t, br, idp, authPath)
	if n := idpHits(idp, "/x/authz"); n != 1 {
		t.Fatalf("IdP authorize hits: %d", n)
	}
	// 2. IdP callback: session is created and the original /authorize URL is resumed
	r, _ := br.get(cbURL)
	if r.StatusCode != 303 || r.Header.Get("Location") != authPath {
		t.Fatalf("callback should resume the original /authorize request: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	// 3. resumed /authorize issues the skgate code to the client redirect, state preserved
	r, _ = br.get(r.Header.Get("Location"))
	if r.StatusCode != 302 {
		t.Fatalf("resumed /authorize: %d", r.StatusCode)
	}
	cl, _ := url.Parse(r.Header.Get("Location"))
	if cl.Scheme+"://"+cl.Host+cl.Path != clientRedirect || cl.Query().Get("state") != "st-xyz" || cl.Query().Get("code") == "" {
		t.Fatalf("client redirect: %s", cl)
	}
	code := cl.Query().Get("code")
	var sub, email string
	if err := a.DB.QueryRow(`SELECT sub,email FROM oauth_codes`).Scan(&sub, &email); err != nil || sub != "u-1" || email != "admin@example.com" {
		t.Fatalf("code must record the OIDC identity: %v %q %q", err, sub, email)
	}
	// 4. token exchange with PKCE (wrong verifier first: code is single use, so use a second flow for that)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {clientID}, "redirect_uri": {clientRedirect}}
	resp, err := http.PostForm(ts.URL+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	var tok map[string]any
	json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	access, _ := tok["access_token"].(string)
	if resp.StatusCode != 200 || !strings.HasPrefix(access, "skat_") {
		t.Fatalf("token: %d %v", resp.StatusCode, tok)
	}
	var n int
	a.DB.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE sub='u-1' AND email='admin@example.com'`).Scan(&n)
	if n != 2 {
		t.Fatalf("tokens must record the identity, got %d", n)
	}
	// 5. call the MCP alias with the access token
	req, _ := http.NewRequest("POST", ts.URL+"/mcp/tools", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "tools/list") {
		t.Fatalf("mcp call with OAuth token: %d %s", resp.StatusCode, body)
	}
	// without a token it is refused
	req.Header.Del("Authorization")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	// 6. existing session: the next /authorize skips the IdP entirely
	before := len(idp.Paths())
	r, _ = br.get(authorizePath(clientID, clientRedirect, challenge))
	loc, _ := url.Parse(r.Header.Get("Location"))
	if r.StatusCode != 302 || loc.Host != "grok.com" || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st-xyz" {
		t.Fatalf("session should skip login: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if len(idp.Paths()) != before {
		t.Fatal("IdP must not be contacted when a session exists")
	}
}

func TestPKCEWrongVerifierAfterSSO(t *testing.T) {
	a, ts, idp := newApp(t, nil)
	_ = a
	clientID := registerClient(t, ts, clientRedirect)
	_, challenge := pkcePair()
	authPath := authorizePath(clientID, clientRedirect, challenge)
	br := newBrowser(t, ts)
	r, _ := br.get(followToIdP(t, br, idp, authPath))
	r, _ = br.get(r.Header.Get("Location"))
	loc, _ := url.Parse(r.Header.Get("Location"))
	resp, _ := http.PostForm(ts.URL+"/token", url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"code_verifier": {strings.Repeat("x", 50)}, "client_id": {clientID}})
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("wrong PKCE verifier must fail: %d", resp.StatusCode)
	}
}

func TestAuthorizeUnconfiguredOIDCNeverIssuesCode(t *testing.T) {
	a, ts, _ := newApp(t, func(c *config.Config, _ *oidctest.Provider) {
		c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret = "", "", ""
	})
	clientID := registerClient(t, ts, clientRedirect)
	_, challenge := pkcePair()
	r, body := newBrowser(t, ts).get(authorizePath(clientID, clientRedirect, challenge))
	if r.StatusCode != 503 || !strings.Contains(body, "OIDC is not configured") || r.Header.Get("Location") != "" {
		t.Fatalf("want error page, got %d %q", r.StatusCode, body)
	}
	var n int
	a.DB.QueryRow(`SELECT COUNT(*) FROM oauth_codes`).Scan(&n)
	if n != 0 {
		t.Fatal("a code was issued without OIDC")
	}
}

func TestAuthorizeDisallowedUserGetsNoCode(t *testing.T) {
	for name, mut := range map[string]func(*config.Config, *oidctest.Provider){
		"email": func(c *config.Config, _ *oidctest.Provider) { c.OIDCEmails = []string{"boss@example.com"} },
		"group": func(c *config.Config, _ *oidctest.Provider) { c.OIDCGroups = []string{"root-admins"} },
	} {
		t.Run(name, func(t *testing.T) {
			a, ts, idp := newApp(t, mut)
			clientID := registerClient(t, ts, clientRedirect)
			_, challenge := pkcePair()
			authPath := authorizePath(clientID, clientRedirect, challenge)
			br := newBrowser(t, ts)
			r, body := br.get(followToIdP(t, br, idp, authPath))
			if r.StatusCode != 403 || !strings.Contains(body, "not allowed") {
				t.Fatalf("disallowed user: %d", r.StatusCode)
			}
			// still no session: /authorize bounces to login again, and no code exists
			r, _ = br.get(authPath)
			if r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), "/admin/oidc/login") {
				t.Fatalf("no session expected: %d %s", r.StatusCode, r.Header.Get("Location"))
			}
			var n int
			a.DB.QueryRow(`SELECT COUNT(*) FROM oauth_codes`).Scan(&n)
			if n != 0 {
				t.Fatal("a code was issued to a disallowed user")
			}
		})
	}
}

func TestAuthorizeRejectsBadRequestsBeforeIdP(t *testing.T) {
	_, ts, idp := newApp(t, nil)
	clientID := registerClient(t, ts, clientRedirect)
	_, challenge := pkcePair()
	br := newBrowser(t, ts)
	for name, p := range map[string]string{
		"other-origin redirect": authorizePath(clientID, "https://evil.example.com/cb", challenge),
		"unregistered redirect": authorizePath(clientID, "https://grok.com/other", challenge),
		"unknown client":        authorizePath("skc-nope", clientRedirect, challenge),
	} {
		r, _ := br.get(p)
		if r.StatusCode != 400 || r.Header.Get("Location") != "" {
			t.Fatalf("%s: want 400 error page, got %d %q", name, r.StatusCode, r.Header.Get("Location"))
		}
	}
	// no PKCE: error redirect to the registered client, not a login bounce
	r, _ := br.get(authorizePath(clientID, clientRedirect, ""))
	loc, _ := url.Parse(r.Header.Get("Location"))
	if r.StatusCode != 302 || loc.Host != "grok.com" || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("missing PKCE: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	// plain PKCE method
	r, _ = br.get(strings.Replace(authorizePath(clientID, clientRedirect, challenge), "S256", "plain", 1))
	loc, _ = url.Parse(r.Header.Get("Location"))
	if r.StatusCode != 302 || loc.Host != "grok.com" || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("plain PKCE: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if len(idp.Paths()) != 0 {
		t.Fatalf("IdP must not be contacted for bad requests: %v", idp.Paths())
	}
}

func TestOIDCLoginNextIsNotAnOpenRedirect(t *testing.T) {
	_, ts, idp := newApp(t, nil)
	for _, evil := range []string{
		"https://evil.example/x", "//evil.example/x", "/\\evil.example", "javascript:alert(1)", "http://evil.example",
		"/admin/oidc/callback", "/admin/logout", "/v1/models", "/authorize/../../x", "\\\\evil.example", "/%0d%0aX: y",
	} {
		br := newBrowser(t, ts)
		r, _ := br.get("/admin/oidc/login?next=" + url.QueryEscape(evil))
		if r.StatusCode != 302 {
			t.Fatalf("%q: %d", evil, r.StatusCode)
		}
		cb := idp.Authorize(t, r.Header.Get("Location"))
		r, _ = br.get(cb)
		if r.StatusCode != 303 || r.Header.Get("Location") != "/admin" {
			t.Fatalf("next=%q must fall back to /admin, got %d %q", evil, r.StatusCode, r.Header.Get("Location"))
		}
	}
	// a legitimate /authorize path is preserved
	br := newBrowser(t, ts)
	want := "/authorize?client_id=c&state=s"
	r, _ := br.get("/admin/oidc/login?next=" + url.QueryEscape(want))
	r, _ = br.get(idp.Authorize(t, r.Header.Get("Location")))
	if r.Header.Get("Location") != want {
		t.Fatalf("legit next lost: %q", r.Header.Get("Location"))
	}
}

func TestConsentAfterLogin(t *testing.T) {
	_, ts, idp := newApp(t, func(c *config.Config, _ *oidctest.Provider) { c.RequireConsent = true })
	clientID := registerClient(t, ts, clientRedirect)
	_, challenge := pkcePair()
	authPath := authorizePath(clientID, clientRedirect, challenge)
	br := newBrowser(t, ts)
	r, _ := br.get(followToIdP(t, br, idp, authPath))
	r, body := br.get(r.Header.Get("Location"))
	if r.StatusCode != 200 || !strings.Contains(body, "Authorize MCP access?") {
		t.Fatalf("consent page expected after login: %d", r.StatusCode)
	}
	csrf := between(body, `name="csrf" value="`, `"`)
	v := url.Values{"csrf": {csrf}, "decision": {"deny"}}
	for k, vv := range map[string]string{"response_type": "code", "client_id": clientID, "redirect_uri": clientRedirect,
		"state": "st-xyz", "code_challenge": challenge, "code_challenge_method": "S256", "scope": "mcp"} {
		v.Set(k, vv)
	}
	r, _ = br.post("/authorize", v)
	if loc, _ := url.Parse(r.Header.Get("Location")); loc.Query().Get("error") != "access_denied" {
		t.Fatalf("deny: %s", r.Header.Get("Location"))
	}
	v.Set("decision", "approve")
	r, _ = br.post("/authorize", v)
	if loc, _ := url.Parse(r.Header.Get("Location")); loc.Query().Get("code") == "" {
		t.Fatalf("approve: %s", r.Header.Get("Location"))
	}
}
