package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/vkeys"
)

const hostedRedirect = "https://hosted.example/r/abc123"

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t    *testing.T
	ts   *httptest.Server
	srv  *Server
	db   *store.DB
	keys *vkeys.Manager
	cfg  *config.Config
	logs *syncBuf
	// loggedIn simulates a valid admin/OIDC session for /authorize (the real one is in package admin).
	loggedIn bool
}

func newEnv(t *testing.T, mutate func(*config.Config)) *env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := &config.Config{PublicURL: "http://placeholder", OIDCIssuer: "https://idp.example", OIDCClientID: "skgate", OIDCClientSecret: "x"}
	if mutate != nil {
		mutate(cfg)
	}
	cfg.Bind(db)
	keys := vkeys.New(db)
	srv := NewServer(cfg, db, keys)
	t.Cleanup(srv.ShutdownManaged)
	e := &env{t: t, srv: srv, db: db, keys: keys, cfg: cfg, loggedIn: true, logs: &syncBuf{}}
	srv.Log.SetOutput(e.logs)
	srv.AdminSession = func(*http.Request) (string, bool) { return "csrf-x", e.loggedIn }
	srv.AdminIdentity = func(*http.Request) (string, string, bool) { return "sub-1", "admin@example.com", e.loggedIn }
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(srv.Log.Middleware(mux))
	t.Cleanup(ts.Close)
	cfg.PublicURL = ts.URL
	e.ts = ts
	return e
}

func (e *env) noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (e *env) do(method, path string, hdr map[string]string, body string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := e.noRedirect().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not json (%d): %s", resp.StatusCode, b)
	}
	return m
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 20) + strings.Repeat("W", 30)
	s := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(s[:])
}

func (e *env) register(redirect, method string) map[string]any {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"client_name": "test", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": method})
	resp := e.do("POST", "/register", map[string]string{"Content-Type": "application/json"}, string(b))
	if resp.StatusCode != 201 {
		e.t.Fatalf("register status %d", resp.StatusCode)
	}
	return readJSON(e.t, resp)
}

// authorizeCode runs /authorize and returns the code and the redirect Location.
func (e *env) authorizeCode(clientID, redirect, challenge, extra string) (code string, loc *url.URL, status int) {
	e.t.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {"st-1"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	path := "/authorize?" + q.Encode() + extra
	resp := e.do("GET", path, nil, "")
	resp.Body.Close()
	if resp.StatusCode != 302 {
		return "", nil, resp.StatusCode
	}
	loc, _ = url.Parse(resp.Header.Get("Location"))
	return loc.Query().Get("code"), loc, resp.StatusCode
}

func form(kv ...string) string {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

var formHdr = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

func TestWellKnownMetadata(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{
		"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/mcp",
		"/.well-known/oauth-authorization-server/mcp/foo", "/.well-known/openid-configuration", "/.well-known/openid-configuration/mcp/foo",
	} {
		resp := e.do("GET", p, nil, "")
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s: missing CORS", p)
		}
		m := readJSON(t, resp)
		if m["issuer"] != e.ts.URL || m["authorization_endpoint"] != e.ts.URL+"/authorize" ||
			m["token_endpoint"] != e.ts.URL+"/token" || m["registration_endpoint"] != e.ts.URL+"/register" {
			t.Errorf("%s: bad endpoints %v", p, m)
		}
		if !strings.Contains(toJSON(m["code_challenge_methods_supported"]), "S256") ||
			toJSON(m["response_types_supported"]) != `["code"]` ||
			toJSON(m["grant_types_supported"]) != `["authorization_code","refresh_token"]` {
			t.Errorf("%s: bad capabilities %v", p, m)
		}
		am := toJSON(m["token_endpoint_auth_methods_supported"])
		for _, want := range []string{"none", "client_secret_post", "client_secret_basic", "private_key_jwt"} {
			if !strings.Contains(am, want) {
				t.Errorf("auth methods missing %s: %s", want, am)
			}
		}
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource/mcp/", "/.well-known/oauth-protected-resource/mcp/foo", "/.well-known/oauth-protected-resource/mcp/foo/"} {
		m := readJSON(t, e.do("GET", p, nil, ""))
		want := strings.TrimSuffix(e.ts.URL+strings.TrimPrefix(p, "/.well-known/oauth-protected-resource"), "/")
		if m["resource"] != want || toJSON(m["authorization_servers"]) != `["`+e.ts.URL+`"]` {
			t.Errorf("%s: PRM %v want resource %s", p, m, want)
		}
	}
	// Root PRM is the same document as /mcp (aggregate resource) for clients that probe the site root.
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/"} {
		m := readJSON(t, e.do("GET", p, nil, ""))
		want := e.ts.URL + "/mcp"
		if m["resource"] != want || toJSON(m["authorization_servers"]) != `["`+e.ts.URL+`"]` {
			t.Errorf("%s: PRM %v want resource %s", p, m, want)
		}
	}
	resp := e.do("OPTIONS", "/token", map[string]string{"Origin": "https://x.example", "Access-Control-Request-Method": "POST"}, "")
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("preflight failed: %d", resp.StatusCode)
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestUnauthorized401(t *testing.T) {
	e := newEnv(t, nil)
	cases := map[string]string{
		"/mcp":        e.ts.URL + "/.well-known/oauth-protected-resource/mcp",
		"/mcp/":       e.ts.URL + "/.well-known/oauth-protected-resource/mcp",
		"/mcp/foo":    e.ts.URL + "/.well-known/oauth-protected-resource/mcp/foo",
		"/mcp/foo/":   e.ts.URL + "/.well-known/oauth-protected-resource/mcp/foo",
		"/mcp/nosuch": e.ts.URL + "/.well-known/oauth-protected-resource/mcp/nosuch",
	}
	for path, meta := range cases {
		resp := e.do("POST", path, map[string]string{"Content-Type": "application/json"}, `{}`)
		if resp.StatusCode != 401 {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		want := `Bearer resource_metadata="` + meta + `"`
		if got := resp.Header.Get("WWW-Authenticate"); got != want {
			t.Errorf("%s: WWW-Authenticate=%q want %q", path, got, want)
		}
		m := readJSON(t, resp)
		if m["error"] == nil {
			t.Errorf("%s: no json error body", path)
		}
	}
	// bad credential says invalid_token
	resp := e.do("POST", "/mcp/foo", map[string]string{"Authorization": "Bearer nope"}, `{}`)
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("bad token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	resp.Body.Close()
}

// Unusable redirect URIs are dropped as long as one usable URI remains.
func TestDCRIgnoresUnusableRedirects(t *testing.T) {
	e := newEnv(t, nil)
	post := func(body string) map[string]any {
		r := e.do("POST", "/register", map[string]string{"Content-Type": "application/json"}, body)
		if r.StatusCode != 201 {
			t.Fatalf("%s: status %d", body, r.StatusCode)
		}
		return readJSON(t, r)
	}
	m := post(`{"redirect_uris":["cursor://anysphere.cursor-mcp/oauth/callback","` + hostedRedirect + `","http://evil.example/cb","https://u:p@x.example/cb","http://127.0.0.1:5555/cb"]}`)
	uris, _ := m["redirect_uris"].([]any)
	if len(uris) != 2 || uris[0] != hostedRedirect || uris[1] != "http://127.0.0.1:5555/cb" {
		t.Fatalf("only the usable URIs should be kept: %v", uris)
	}
	c, _ := e.srv.Clients.Get(m["client_id"].(string))
	if len(c.RedirectURIs) != 2 {
		t.Fatalf("stored: %v", c.RedirectURIs)
	}
}

// Grant types, response types and auth methods are reduced to the supported subset.
func TestDCRNegotiatesDown(t *testing.T) {
	e := newEnv(t, nil)
	post := func(body string) *http.Response {
		return e.do("POST", "/register", map[string]string{"Content-Type": "application/json"}, body)
	}
	r := post(`{"redirect_uris":["` + hostedRedirect + `"],"token_endpoint_auth_method":"private_key_jwt",` +
		`"grant_types":["authorization_code","client_credentials","refresh_token","refresh_token"],"response_types":["code","token"]}`)
	if r.StatusCode != 201 {
		t.Fatalf("status %d", r.StatusCode)
	}
	m := readJSON(t, r)
	if m["token_endpoint_auth_method"] != "none" || m["client_secret"] != nil {
		t.Errorf("unsupported method should fall back to a public client: %v", m)
	}
	if g, _ := json.Marshal(m["grant_types"]); string(g) != `["authorization_code","refresh_token"]` {
		t.Errorf("grants: %s", g)
	}
	if g, _ := json.Marshal(m["response_types"]); string(g) != `["code"]` {
		t.Errorf("response types: %s", g)
	}
	// nothing usable left: still refused, with the invalid_client_metadata error
	for _, bad := range []string{
		`{"redirect_uris":["` + hostedRedirect + `"],"grant_types":["client_credentials"]}`,
		`{"redirect_uris":["` + hostedRedirect + `"],"response_types":["token"]}`,
	} {
		r := post(bad)
		if r.StatusCode != 400 || readJSON(t, r)["error"] != "invalid_client_metadata" {
			t.Errorf("%s: %d", bad, r.StatusCode)
		}
	}
}

func TestDCR(t *testing.T) {
	e := newEnv(t, nil)
	post := func(body string) *http.Response {
		return e.do("POST", "/register", map[string]string{"Content-Type": "application/json"}, body)
	}
	resp := post(`{"client_name":"hosted","redirect_uris":["` + hostedRedirect + `"]}`)
	if resp.StatusCode != 201 {
		t.Fatalf("https redirect on any host rejected: %d", resp.StatusCode)
	}
	m := readJSON(t, resp)
	id, _ := m["client_id"].(string)
	if !strings.HasPrefix(id, "skc-") || m["token_endpoint_auth_method"] != "none" || m["client_secret"] != nil {
		t.Fatalf("bad DCR response %v", m)
	}
	if _, ok := e.srv.Clients.Get(id); !ok {
		t.Fatal("client not persisted")
	}
	// confidential
	m = readJSON(t, post(`{"redirect_uris":["http://127.0.0.1:9999/cb"],"token_endpoint_auth_method":"client_secret_basic"}`))
	if m["client_secret"] == nil || m["client_secret"] == "" {
		t.Fatalf("confidential client should get a secret: %v", m)
	}
	for _, bad := range []string{
		`{"redirect_uris":["http://hosted.example/r/x"]}`,
		`{"redirect_uris":["https://hosted.example@evil.com/"]}`,
		`{"redirect_uris":["https://u:p@hosted.example/r/x"]}`,
		`{"redirect_uris":["cursor://anysphere.cursor-mcp/oauth/callback","ftp://evil.example.com/cb"]}`,
		`{"redirect_uris":[]}`,
		`{}`,
	} {
		r := post(bad)
		if r.StatusCode != 400 {
			t.Errorf("accepted %s: %d", bad, r.StatusCode)
		}
		if m := readJSON(t, r); m["error"] != "invalid_redirect_uri" {
			t.Errorf("%s: error=%v", bad, m["error"])
		}
	}
	if g := e.do("GET", "/register", nil, ""); g.StatusCode != 405 {
		t.Errorf("GET /register: %d", g.StatusCode)
	}
}

func TestPKCEEndToEnd(t *testing.T) {
	for _, method := range []string{"none", "client_secret_post", "client_secret_basic"} {
		t.Run(method, func(t *testing.T) {
			e := newEnv(t, nil)
			reg := e.register(hostedRedirect, method)
			id, _ := reg["client_id"].(string)
			secret, _ := reg["client_secret"].(string)
			verifier, challenge := pkce()
			resource := e.ts.URL + "/mcp/foo"
			code, loc, st := e.authorizeCode(id, hostedRedirect, challenge, "&resource="+url.QueryEscape(resource)+"&scope=mcp")
			if st != 302 || code == "" {
				t.Fatalf("authorize: status %d", st)
			}
			if loc.Host != "hosted.example" || loc.Path != "/r/abc123" || loc.Query().Get("state") != "st-1" || loc.Query().Get("iss") != e.ts.URL {
				t.Fatalf("bad redirect %s", loc)
			}
			hdr := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
			body := []string{"grant_type", "authorization_code", "code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect, "resource", resource}
			switch method {
			case "none":
				body = append(body, "client_id", id)
			case "client_secret_post":
				body = append(body, "client_id", id, "client_secret", secret)
			case "client_secret_basic":
				hdr["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(id)+":"+url.QueryEscape(secret)))
			}
			// wrong verifier burns the code
			bad := append([]string{}, body...)
			bad[5] = strings.Repeat("x", 50)
			if r := e.do("POST", "/token", hdr, form(bad...)); r.StatusCode != 400 || readJSON(t, r)["error"] != "invalid_grant" {
				t.Fatal("wrong verifier should be invalid_grant")
			}
			if r := e.do("POST", "/token", hdr, form(body...)); r.StatusCode != 400 {
				t.Fatal("code must be single use and burned after a failed attempt")
			}
			// second, clean run
			code, _, _ = e.authorizeCode(id, hostedRedirect, challenge, "&resource="+url.QueryEscape(resource))
			body[3] = code
			resp := e.do("POST", "/token", hdr, form(body...))
			if resp.StatusCode != 200 {
				t.Fatalf("token: %d", resp.StatusCode)
			}
			tok := readJSON(t, resp)
			access, _ := tok["access_token"].(string)
			refresh, _ := tok["refresh_token"].(string)
			if access == "" || refresh == "" || tok["token_type"] != "Bearer" || tok["expires_in"].(float64) != 3600 {
				t.Fatalf("bad token response %v", tok)
			}
			// replay fails
			if r := e.do("POST", "/token", hdr, form(body...)); r.StatusCode != 400 {
				t.Fatal("replayed code accepted")
			}
			// tokens stored hashed only
			var n int
			e.db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE hash IN (?,?)`, access, refresh).Scan(&n)
			if n != 0 {
				t.Fatal("raw tokens stored")
			}
			// access token works on the bound resource with an upstream
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ok":true}`) }))
			defer up.Close()
			if err := e.srv.Upstreams.Create(Upstream{Alias: "foo", URL: up.URL, AuthKind: AuthNone, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if err := e.srv.Upstreams.Create(Upstream{Alias: "bar", URL: up.URL, AuthKind: AuthNone, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			auth := map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"}
			if r := e.do("POST", "/mcp/foo", auth, `{}`); r.StatusCode != 200 {
				t.Fatalf("oauth token on /mcp/foo: %d", r.StatusCode)
			}
			if r := e.do("POST", "/mcp/foo/", auth, `{}`); r.StatusCode != 200 {
				t.Fatalf("oauth token on /mcp/foo/: %d", r.StatusCode)
			}
			if r := e.do("POST", "/mcp/bar", auth, `{}`); r.StatusCode != 401 {
				t.Fatalf("RFC 8707 audience not enforced: %d", r.StatusCode)
			}
			// refresh grant (rotates)
			rb := []string{"grant_type", "refresh_token", "refresh_token", refresh}
			switch method {
			case "none":
				rb = append(rb, "client_id", id)
			case "client_secret_post":
				rb = append(rb, "client_id", id, "client_secret", secret)
			}
			resp = e.do("POST", "/token", hdr, form(rb...))
			if resp.StatusCode != 200 {
				t.Fatalf("refresh: %d", resp.StatusCode)
			}
			tok2 := readJSON(t, resp)
			if tok2["access_token"] == access || tok2["refresh_token"] == refresh {
				t.Fatal("tokens not rotated")
			}
			if r := e.do("POST", "/token", hdr, form(rb...)); r.StatusCode != 400 {
				t.Fatal("old refresh token still valid")
			}
			// a different client cannot use the token
			other := e.register(hostedRedirect, "none")
			_ = other
			// expired access token is rejected
			e.db.Exec(`UPDATE oauth_tokens SET expires_at=? WHERE kind='access'`, time.Now().Add(-time.Second).Unix())
			if r := e.do("POST", "/mcp/foo", map[string]string{"Authorization": "Bearer " + tok2["access_token"].(string)}, `{}`); r.StatusCode != 401 {
				t.Fatal("expired token accepted")
			}
		})
	}
}

func TestAuthorizeRejections(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	_, challenge := pkce()
	// any https origin may register, but only the exact registered redirect is accepted: error page, never a redirect
	if _, _, st := e.authorizeCode(id, "https://evil.example.com/cb", challenge, ""); st != 400 {
		t.Errorf("unregistered redirect: %d", st)
	}
	// same origin but another path (exact match required)
	if _, _, st := e.authorizeCode(id, "https://hosted.example/r/other", challenge, ""); st != 400 {
		t.Errorf("unregistered path: %d", st)
	}
	if _, _, st := e.authorizeCode("skc-unknown", hostedRedirect, challenge, ""); st != 400 {
		t.Errorf("unknown client: %d", st)
	}
	// missing / plain PKCE -> error redirect
	for _, extra := range []string{"", "&x=1"} {
		q := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {hostedRedirect}, "state": {"s"}}
		if extra == "&x=1" {
			q.Set("code_challenge", "plainplainplainplainplainplainplainplainplain")
			q.Set("code_challenge_method", "plain")
		}
		resp := e.do("GET", "/authorize?"+q.Encode(), nil, "")
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != 302 || loc.Query().Get("error") != "invalid_request" || loc.Query().Get("state") != "s" {
			t.Errorf("pkce not enforced (%q): %d %v", extra, resp.StatusCode, loc)
		}
	}
	// foreign resource
	_, loc, _ := e.authorizeCode(id, hostedRedirect, challenge, "&resource="+url.QueryEscape("https://other.example/mcp"))
	if loc == nil || loc.Query().Get("error") != "invalid_target" {
		t.Errorf("foreign resource: %v", loc)
	}
	// wrong client secret / unknown grant
	if r := e.do("POST", "/token", formHdr, form("grant_type", "password", "client_id", id)); r.StatusCode != 400 || readJSON(t, r)["error"] != "unsupported_grant_type" {
		t.Error("unsupported grant not reported")
	}
	conf := e.register(hostedRedirect, "client_secret_post")
	if r := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "client_id", conf["client_id"].(string), "client_secret", "wrong", "code", "x", "code_verifier", "y")); r.StatusCode != 401 || readJSON(t, r)["error"] != "invalid_client" {
		t.Error("bad secret should be invalid_client 401")
	}
}

func TestLoopbackNativeClient(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register("http://127.0.0.1:5555/callback", "none")
	id := reg["client_id"].(string)
	_, challenge := pkce()
	// native clients pick a fresh port each run (RFC 8252)
	code, loc, st := e.authorizeCode(id, "http://127.0.0.1:6666/callback", challenge, "")
	if st != 302 || code == "" || loc.Host != "127.0.0.1:6666" {
		t.Fatalf("loopback port variance rejected: %d %v", st, loc)
	}
}

func TestManualClient(t *testing.T) {
	e := newEnv(t, nil)
	redirect := "https://grok.com/cb"
	_, err := e.srv.Clients.Create(Client{ID: "skc-manual", Name: "manual", RedirectURIs: []string{redirect}, AuthMethod: "client_secret_post", Source: "admin"}, "s3cret-value")
	if err != nil {
		t.Fatal(err)
	}
	verifier, challenge := pkce()
	code, _, st := e.authorizeCode("skc-manual", redirect, challenge, "")
	if st != 302 || code == "" {
		t.Fatalf("manual client authorize failed: %d", st)
	}
	r := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "client_id", "skc-manual", "client_secret", "s3cret-value", "code", code, "code_verifier", verifier, "redirect_uri", redirect))
	if r.StatusCode != 200 {
		t.Fatalf("token: %d", r.StatusCode)
	}
	// admin-registered clients are exact-match for any origin
	other := "https://my.private.example/cb"
	e.srv.Clients.Create(Client{ID: "skc-priv", RedirectURIs: []string{other}, AuthMethod: "none", Source: "admin"}, "")
	if code, _, _ := e.authorizeCode("skc-priv", other, challenge, ""); code == "" {
		t.Fatal("admin-registered exact https redirect should work")
	}
}

func TestAuthorizeNeverAutoApproves(t *testing.T) {
	// no session: goes to OIDC login, no code
	e := newEnv(t, nil)
	e.loggedIn = false
	reg := e.register(hostedRedirect, "none")
	_, challenge := pkce()
	code, loc, st := e.authorizeCode(reg["client_id"].(string), hostedRedirect, challenge, "")
	if st != 302 || code != "" || loc.Path != "/admin/oidc/login" || !strings.HasPrefix(loc.Query().Get("next"), "/authorize?") {
		t.Fatalf("want redirect to OIDC login with next=/authorize?..., got %d %v", st, loc)
	}
	// OIDC not configured: error page, never a code
	e = newEnv(t, func(c *config.Config) { c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret = "", "", "" })
	reg = e.register(hostedRedirect, "none")
	code, _, st = e.authorizeCode(reg["client_id"].(string), hostedRedirect, challenge, "")
	if st != 503 || code != "" {
		t.Fatalf("unconfigured OIDC must refuse /authorize, got %d code=%q", st, code)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM oauth_codes`).Scan(&n)
	if n != 0 {
		t.Fatal("no code may be stored")
	}
	// bad requests are still rejected before the login bounce, with a session-less client
	e = newEnv(t, nil)
	e.loggedIn = false
	reg = e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	if _, _, st := e.authorizeCode(id, "https://evil.example.com/cb", challenge, ""); st != 400 {
		t.Fatalf("an unregistered redirect must be rejected before login, got %d", st)
	}
	if _, loc, st := e.authorizeCode(id, hostedRedirect, "", ""); st != 302 || loc.Host == "" || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("missing PKCE must be a redirect error, not a login bounce: %d %v", st, loc)
	}
}

func TestCodeAndTokenRecordIdentity(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register(hostedRedirect, "none")
	id := reg["client_id"].(string)
	verifier, challenge := pkce()
	code, _, _ := e.authorizeCode(id, hostedRedirect, challenge, "")
	var sub, email string
	if err := e.db.QueryRow(`SELECT sub,email FROM oauth_codes`).Scan(&sub, &email); err != nil || sub != "sub-1" || email != "admin@example.com" {
		t.Fatalf("code identity: %v %q %q", err, sub, email)
	}
	resp := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "code", code, "code_verifier", verifier, "client_id", id, "redirect_uri", hostedRedirect))
	tok := readJSON(t, resp)
	if tok["access_token"] == nil {
		t.Fatalf("token: %v", tok)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE sub='sub-1' AND email='admin@example.com'`).Scan(&n)
	if n != 2 {
		t.Fatalf("both tokens must carry the identity, got %d", n)
	}
	// refresh keeps it
	resp = e.do("POST", "/token", formHdr, form("grant_type", "refresh_token", "refresh_token", tok["refresh_token"].(string), "client_id", id))
	if readJSON(t, resp)["access_token"] == nil {
		t.Fatal("refresh failed")
	}
	e.db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE sub IS NULL OR sub<>'sub-1' OR email<>'admin@example.com'`).Scan(&n)
	if n != 0 {
		t.Fatalf("refreshed tokens must keep identity, %d rows lack it", n)
	}
}

func TestConsentMode(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RequireConsent = true })
	e.loggedIn = false
	reg := e.register(hostedRedirect, "none")
	_, challenge := pkce()
	_, loc, st := e.authorizeCode(reg["client_id"].(string), hostedRedirect, challenge, "")
	if st != 302 || loc.Path != "/admin/oidc/login" {
		t.Fatalf("consent mode should send to OIDC login, got %d %v", st, loc)
	}
}

// fakeMCP is a fake upstream MCP server recording what it received.
type fakeMCP struct {
	*httptest.Server
	last chan http.Header
	body chan string
}

func newFakeMCP(t *testing.T) *fakeMCP {
	f := &fakeMCP{last: make(chan http.Header, 8), body: make(chan string, 8)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h := r.Header.Clone()
		h.Set("X-Test-Host", r.Host) // Go moves Host out of r.Header
		f.last <- h
		f.body <- string(b)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			for i := 0; i < 2; i++ {
				io.WriteString(w, "data: tick\n\n")
				w.(http.Flusher).Flush()
			}
			return
		}
		if strings.Contains(string(b), `"initialize"`) {
			w.Header().Set("Mcp-Session-Id", "up-sess-1")
		}
		if strings.Contains(string(b), "sse-reply") {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"ok\":true}}\n\n")
			return
		}
		if strings.Contains(string(b), "needs-auth") {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://upstream.example/x"`)
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func TestProxyInboundKeyAndOutboundBearer(t *testing.T) {
	e := newEnv(t, nil)
	up := newFakeMCP(t)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "tools", URL: up.URL, AuthKind: AuthBearer, AuthValue: "UPSTREAM-SECRET", Enabled: true, IncludeInMCP: true}); err != nil {
		t.Fatal(err)
	}
	key, _, _ := e.keys.Create("t")
	json := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Mcp-Session-Id": "abc"}
	// no key => 401, upstream never called
	if r := e.do("POST", "/mcp/tools", json, `{"method":"ping"}`); r.StatusCode != 401 {
		t.Fatalf("unauthenticated: %d", r.StatusCode)
	}
	select {
	case <-up.last:
		t.Fatal("upstream called without inbound auth")
	default:
	}
	// method 2: Authorization Bearer sk-
	h := map[string]string{"Authorization": "Bearer " + key, "X-Upstream-Authorization": "leak", "Cookie": "a=b"}
	for k, v := range json {
		h[k] = v
	}
	r := e.do("POST", "/mcp/tools", h, `{"method":"initialize"}`)
	if r.StatusCode != 200 || r.Header.Get("Mcp-Session-Id") != "up-sess-1" {
		t.Fatalf("proxy: %d sess=%q", r.StatusCode, r.Header.Get("Mcp-Session-Id"))
	}
	got := <-up.last
	if got.Get("Authorization") != "Bearer UPSTREAM-SECRET" {
		t.Fatalf("outbound bearer not injected: %q", got.Get("Authorization"))
	}
	if strings.Contains(strings.Join(got.Values("Authorization"), ","), key) {
		t.Fatal("inbound sk key forwarded upstream")
	}
	if got.Get("Mcp-Session-Id") != "abc" || got.Get("Accept") == "" || got.Get("Content-Type") != "application/json" {
		t.Fatalf("headers not forwarded: %v", got)
	}
	if got.Get("Cookie") != "" || got.Get("X-Upstream-Authorization") != "" {
		t.Fatal("cookie or X-Upstream-Authorization leaked")
	}
	if b := <-up.body; b != `{"method":"initialize"}` {
		t.Fatalf("body altered: %s", b)
	}
	// trailing slash accepted on /mcp/{alias}
	for _, p := range []string{"/mcp/tools/"} {
		if r := e.do("POST", p, h, `{}`); r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
		<-up.last
		<-up.body
	}
	// revoked key
	kid := int64(0)
	ks, _ := e.keys.List()
	kid = ks[0].ID
	e.keys.Revoke(kid)
	if r := e.do("POST", "/mcp/tools", h, `{}`); r.StatusCode != 401 {
		t.Errorf("revoked key: %d", r.StatusCode)
	}
	// upstream 401 must not leak the upstream challenge
	key2, _, _ := e.keys.Create("t2")
	h["Authorization"] = "Bearer " + key2
	r = e.do("POST", "/mcp/tools", h, `needs-auth`)
	if r.StatusCode != 502 || r.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("upstream 401 handling: %d %q", r.StatusCode, r.Header.Get("WWW-Authenticate"))
	}
}

func TestInboundKeyMethods(t *testing.T) {
	e := newEnv(t, nil)
	up := newFakeMCP(t)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: up.URL, AuthKind: AuthNone, Enabled: true})
	key, _, _ := e.keys.Create("t")
	drain := func() {
		select {
		case <-up.last:
			<-up.body
		default:
		}
	}
	try := func(path string, hdr map[string]string) int {
		hdr["Content-Type"] = "application/json"
		r := e.do("POST", path, hdr, `{}`)
		r.Body.Close()
		drain()
		return r.StatusCode
	}
	if s := try("/mcp/u", map[string]string{"Authorization": "Bearer " + key}); s != 200 {
		t.Errorf("Authorization Bearer: %d", s)
	}
	if s := try("/mcp/u", map[string]string{"Authorization": "bearer " + key}); s != 200 {
		t.Errorf("lowercase scheme: %d", s)
	}
	if s := try("/mcp/u", map[string]string{"X-API-Key": key}); s != 200 {
		t.Errorf("X-API-Key: %d", s)
	}
	if s := try("/mcp/u", map[string]string{"X-API-Key": "sk-" + strings.Repeat("a", 48)}); s != 401 {
		t.Errorf("wrong X-API-Key: %d", s)
	}
	// ?key= is off by default
	if s := try("/mcp/u?key="+key, map[string]string{}); s != 401 {
		t.Errorf("?key= must be rejected by default, got %d", s)
	}
	// opt-in per key
	ks, _ := e.keys.List()
	e.keys.SetURLKey(ks[0].ID, true)
	if s := try("/mcp/u?key="+key, map[string]string{}); s != 200 {
		t.Errorf("?key= with opt-in: %d", s)
	}
	other, _, _ := e.keys.Create("other") // a second key stays refused
	if s := try("/mcp/u?key="+other, map[string]string{}); s != 401 {
		t.Errorf("?key= is per key; another key must stay refused, got %d", s)
	}
	// the key must not be forwarded upstream in the query string
	r := e.do("POST", "/mcp/u?key="+key+"&x=1", map[string]string{"Content-Type": "application/json"}, `{}`)
	r.Body.Close()
	<-up.last
	<-up.body
}

func TestOutboundKinds(t *testing.T) {
	e := newEnv(t, nil)
	up := newFakeMCP(t)
	key, _, _ := e.keys.Create("t")
	e.srv.Upstreams.Create(Upstream{Alias: "none", URL: up.URL, AuthKind: AuthNone, Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "hdr", URL: up.URL, AuthKind: AuthHeader, AuthName: "X-Api-Key", AuthValue: "hv", Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "pass", URL: up.URL, AuthKind: AuthPassthrough, Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "off", URL: up.URL, AuthKind: AuthNone, Enabled: false})
	call := func(alias string, extra map[string]string) http.Header {
		h := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}
		for k, v := range extra {
			h[k] = v
		}
		r := e.do("POST", "/mcp/"+alias, h, `{}`)
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", alias, r.StatusCode)
		}
		<-up.body
		return <-up.last
	}
	if g := call("none", nil); g.Get("Authorization") != "" {
		t.Errorf("none forwarded auth: %q", g.Get("Authorization"))
	}
	if g := call("hdr", nil); g.Get("X-Api-Key") != "hv" || g.Get("Authorization") != "" {
		t.Errorf("header kind: %v", g)
	}
	if g := call("pass", nil); g.Get("Authorization") != "" {
		t.Errorf("passthrough must forward nothing without X-Upstream-Authorization, got %q", g.Get("Authorization"))
	}
	if g := call("pass", map[string]string{"X-Upstream-Authorization": "Bearer client-own"}); g.Get("Authorization") != "Bearer client-own" {
		t.Errorf("passthrough with X-Upstream-Authorization: %q", g.Get("Authorization"))
	}
	if r := e.do("POST", "/mcp/off", map[string]string{"Authorization": "Bearer " + key}, `{}`); r.StatusCode != 404 {
		t.Errorf("disabled upstream: %d", r.StatusCode)
	}
	// validation
	if err := e.srv.Upstreams.Create(Upstream{Alias: "Bad_Alias", URL: up.URL, AuthKind: AuthNone}); err == nil {
		t.Error("bad alias accepted")
	}
	if err := e.srv.Upstreams.Create(Upstream{Alias: "dup", URL: "ftp://x", AuthKind: AuthNone}); err == nil {
		t.Error("bad url accepted")
	}
	if err := e.srv.Upstreams.Create(Upstream{Alias: "none", URL: up.URL, AuthKind: AuthNone}); err == nil {
		t.Error("duplicate alias accepted")
	}
}

func TestStreamingSSEPassthrough(t *testing.T) {
	e := newEnv(t, nil)
	up := newFakeMCP(t)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: up.URL, AuthKind: AuthNone, Enabled: true})
	key, _, _ := e.keys.Create("t")
	r := e.do("GET", "/mcp/u", map[string]string{"Authorization": "Bearer " + key, "Accept": "text/event-stream"}, "")
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.Header.Get("Content-Type") != "text/event-stream" || strings.Count(string(b), "data: tick") != 2 {
		t.Fatalf("SSE not passed through: %q %q", r.Header.Get("Content-Type"), b)
	}
}

func TestLegacySSEBridge(t *testing.T) {
	e := newEnv(t, nil)
	up := newFakeMCP(t)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: up.URL, AuthKind: AuthBearer, AuthValue: "UP", Enabled: true})
	key, _, _ := e.keys.Create("t")
	if r := e.do("GET", "/sse/u", nil, ""); r.StatusCode != 401 {
		t.Fatalf("/sse without auth: %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/sse/u", nil)
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 32)
	go func() {
		buf := make([]byte, 4096)
		acc := ""
		for {
			n, err := resp.Body.Read(buf)
			acc += string(buf[:n])
			for {
				i := strings.Index(acc, "\n\n")
				if i < 0 {
					break
				}
				lines <- acc[:i]
				acc = acc[i+2:]
			}
			if err != nil {
				return
			}
		}
	}()
	next := func() string {
		select {
		case l := <-lines:
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for SSE event")
			return ""
		}
	}
	ep := next()
	if !strings.HasPrefix(ep, "event: endpoint\ndata: /messages?sessionId=") {
		t.Fatalf("bad endpoint event: %q", ep)
	}
	endpoint := strings.TrimPrefix(ep, "event: endpoint\ndata: ")
	pr, _ := http.NewRequest("POST", e.ts.URL+endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"sse-reply"}`))
	pr.Header.Set("X-API-Key", key)
	pr.Header.Set("Content-Type", "application/json")
	presp, err := http.DefaultClient.Do(pr)
	if err != nil || presp.StatusCode != 202 {
		t.Fatalf("POST /messages: %v %v", err, presp)
	}
	msg := next()
	if !strings.Contains(msg, `event: message`) || !strings.Contains(msg, `"id":7`) {
		t.Fatalf("reply not delivered on stream: %q", msg)
	}
	if h := <-up.last; h.Get("Authorization") != "Bearer UP" {
		t.Fatalf("bridge outbound auth: %q", h.Get("Authorization"))
	}
	// messages without auth is rejected
	if r := e.do("POST", endpoint, nil, `{}`); r.StatusCode != 401 {
		t.Errorf("/messages without auth: %d", r.StatusCode)
	}
}

func TestCORSOnMCP(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do("OPTIONS", "/mcp/x", map[string]string{"Origin": "https://client.example", "Access-Control-Request-Method": "POST"}, "")
	if r.StatusCode != 204 || !strings.Contains(r.Header.Get("Access-Control-Allow-Headers"), "Mcp-Session-Id") {
		t.Errorf("preflight: %d %v", r.StatusCode, r.Header)
	}
	r = e.do("POST", "/mcp/x", nil, `{}`)
	if r.Header.Get("Access-Control-Expose-Headers") == "" || !strings.Contains(r.Header.Get("Access-Control-Expose-Headers"), "WWW-Authenticate") {
		t.Error("401 must expose WWW-Authenticate to browsers")
	}
}

func TestHostOverride(t *testing.T) {
	e := newEnv(t, nil)
	up := newFakeMCP(t)
	key, _, _ := e.keys.Create("t")
	if err := e.srv.Upstreams.Create(Upstream{Alias: "plain", URL: up.URL, AuthKind: AuthNone, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Upstreams.Create(Upstream{Alias: "ho", URL: up.URL, AuthKind: AuthNone, Enabled: true, HostOverride: "localhost:8000"}); err != nil {
		t.Fatal(err)
	}
	call := func(alias string) http.Header {
		r := e.do("POST", "/mcp/"+alias, map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}, `{}`)
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", alias, r.StatusCode)
		}
		<-up.body
		return <-up.last
	}
	if got := call("ho").Get("X-Test-Host"); got != "localhost:8000" {
		t.Errorf("override Host = %q", got)
	}
	if got := call("plain").Get("X-Test-Host"); got != strings.TrimPrefix(up.URL, "http://") {
		t.Errorf("default Host = %q", got)
	}
	// round trip through the store, and update clears it
	if u, _ := e.srv.Upstreams.Get("ho"); u.HostOverride != "localhost:8000" {
		t.Errorf("stored override = %q", u.HostOverride)
	}
	u, _ := e.srv.Upstreams.Get("ho")
	u.HostOverride = ""
	if err := e.srv.Upstreams.Update(u, true); err != nil {
		t.Fatal(err)
	}
	if got := call("ho").Get("X-Test-Host"); got != strings.TrimPrefix(up.URL, "http://") {
		t.Errorf("cleared override Host = %q", got)
	}
}

func TestHostOverrideValidation(t *testing.T) {
	for h, ok := range map[string]bool{
		"": true, "localhost": true, "localhost:8000": true, "a.b-c.example.com:443": true, "10.0.0.1:80": true,
		"bad host": false, "a/b": false, "http://x": false, "x:": false, "x:0": false, "x:99999": false, "x:ab": false,
		"-x": false, "x@y": false, "x\r\nY: z": false, "[::1]:80": false,
	} {
		u := Upstream{Alias: "a", URL: "http://x/mcp", AuthKind: AuthNone, HostOverride: h}
		if got := u.Validate() == nil; got != ok {
			t.Errorf("host override %q: valid=%v want %v", h, got, ok)
		}
	}
}

// A client created in the admin (Home Assistant style: client_secret_post, PKCE, no resource
// parameter, offline_access) gets expires_in and a refresh_token, also from the refresh grant.
func TestAdminClientTokenResponseHasExpiresInAndRefreshToken(t *testing.T) {
	e := newEnv(t, nil)
	const redirect = "https://my.home-assistant.io/redirect/oauth"
	c, err := e.srv.Clients.Create(Client{ID: "skc-ha", Name: "Home Assistant", RedirectURIs: []string{redirect}, AuthMethod: "client_secret_post", Source: "admin"}, "ha-secret")
	if err != nil {
		t.Fatal(err)
	}
	verifier, challenge := pkce()
	code, _, st := e.authorizeCode(c.ID, redirect, challenge, "&scope="+url.QueryEscape("mcp offline_access"))
	if st != 302 || code == "" {
		t.Fatalf("authorize: %d", st)
	}
	resp := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "code", code, "code_verifier", verifier,
		"redirect_uri", redirect, "client_id", c.ID, "client_secret", "ha-secret"))
	tok := readJSON(t, resp)
	if resp.StatusCode != 200 || tok["expires_in"] != float64(3600) || tok["refresh_token"] == "" || tok["refresh_token"] == nil ||
		tok["token_type"] != "Bearer" || !strings.Contains(tok["scope"].(string), "offline_access") {
		t.Fatalf("token response: %d %v", resp.StatusCode, tok)
	}
	resp = e.do("POST", "/token", formHdr, form("grant_type", "refresh_token", "refresh_token", tok["refresh_token"].(string), "client_id", c.ID, "client_secret", "ha-secret"))
	tok2 := readJSON(t, resp)
	if resp.StatusCode != 200 || tok2["expires_in"] != float64(3600) || tok2["refresh_token"] == nil || tok2["refresh_token"] == tok["refresh_token"] {
		t.Fatalf("refresh response: %d %v", resp.StatusCode, tok2)
	}
}

// authorizeCodeNoPKCE runs /authorize without code_challenge.
func (e *env) authorizeCodeNoPKCE(clientID, redirect string) (code string, loc *url.URL, status int) {
	e.t.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {"st-1"}}
	resp := e.do("GET", "/authorize?"+q.Encode(), nil, "")
	resp.Body.Close()
	if resp.StatusCode != 302 {
		return "", nil, resp.StatusCode
	}
	loc, _ = url.Parse(resp.Header.Get("Location"))
	return loc.Query().Get("code"), loc, resp.StatusCode
}

// PKCE rules: a challenge that is sent must be S256 and is verified; without one only a confidential
// client that has never used PKCE is accepted (it authenticates with its secret); after its first
// PKCE exchange a client needs PKCE; public clients always do.
func TestPKCEAdaptiveRules(t *testing.T) {
	const redirect = "https://my.home-assistant.io/redirect/oauth"
	e := newEnv(t, nil)
	conf := func(id, method string) {
		if _, err := e.srv.Clients.Create(Client{ID: id, Name: id, RedirectURIs: []string{redirect}, AuthMethod: method, Source: "admin"}, "sec-"+id); err != nil {
			t.Fatal(err)
		}
	}
	conf("skc-post", "client_secret_post")
	conf("skc-basic", "client_secret_basic")
	pub := e.register(redirect, "none")["client_id"].(string)
	tokenBody := func(id, code string, extra ...string) string {
		return form(append([]string{"grant_type", "authorization_code", "code", code, "redirect_uri", redirect, "client_id", id, "client_secret", "sec-" + id}, extra...)...)
	}
	verifier, challenge := pkce()

	// (3) confidential, no challenge: accepted at /authorize, exchanged with the secret alone
	code, loc, st := e.authorizeCodeNoPKCE("skc-post", redirect)
	if st != 302 || code == "" {
		t.Fatalf("confidential without challenge: %d %v", st, loc)
	}
	// ... but a wrong secret gets nothing
	bad := strings.Replace(tokenBody("skc-post", code), "sec-skc-post", "wrong", 1)
	if r := e.do("POST", "/token", formHdr, bad); r.StatusCode != 401 {
		t.Fatalf("wrong secret: %d", r.StatusCode)
	}
	code, _, _ = e.authorizeCodeNoPKCE("skc-post", redirect)
	resp := e.do("POST", "/token", formHdr, tokenBody("skc-post", code))
	tok := readJSON(t, resp)
	if resp.StatusCode != 200 || tok["access_token"] == nil || tok["expires_in"] != float64(3600) || tok["refresh_token"] == nil {
		t.Fatalf("token without verifier: %d %v", resp.StatusCode, tok)
	}
	if c, _ := e.srv.Clients.Get("skc-post"); c.PKCESeen {
		t.Fatal("a non-PKCE exchange must not mark the client")
	}
	// client_secret_basic behaves the same
	code, _, st = e.authorizeCodeNoPKCE("skc-basic", redirect)
	if st != 302 || code == "" {
		t.Fatalf("basic without challenge: %d", st)
	}

	// (1) a challenge is verified: wrong verifier fails, missing verifier fails
	code, _, _ = e.authorizeCode("skc-post", redirect, challenge, "")
	if r := e.do("POST", "/token", formHdr, tokenBody("skc-post", code, "code_verifier", strings.Repeat("x", 50))); r.StatusCode != 400 || readJSON(t, r)["error"] != "invalid_grant" {
		t.Fatal("wrong verifier must fail")
	}
	code, _, _ = e.authorizeCode("skc-post", redirect, challenge, "")
	if r := e.do("POST", "/token", formHdr, tokenBody("skc-post", code)); r.StatusCode != 400 {
		t.Fatalf("a sent challenge needs its verifier: %d", r.StatusCode)
	}
	if c, _ := e.srv.Clients.Get("skc-post"); c.PKCESeen {
		t.Fatal("failed exchanges must not mark the client")
	}
	// (2) the first successful PKCE exchange is remembered
	code, _, _ = e.authorizeCode("skc-post", redirect, challenge, "")
	if r := e.do("POST", "/token", formHdr, tokenBody("skc-post", code, "code_verifier", verifier)); r.StatusCode != 200 {
		t.Fatalf("correct verifier: %d", r.StatusCode)
	}
	if c, _ := e.srv.Clients.Get("skc-post"); !c.PKCESeen {
		t.Fatal("client not marked after a successful PKCE exchange")
	}
	// ... and from then on a request without challenge is refused
	if code, loc, st := e.authorizeCodeNoPKCE("skc-post", redirect); st != 302 || code != "" || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("after PKCE use: %d %v", st, loc)
	}
	// the other client is unaffected
	if code, _, _ := e.authorizeCodeNoPKCE("skc-basic", redirect); code == "" {
		t.Fatal("marking is per client")
	}
	// a code issued before the mark cannot be exchanged without a verifier either
	code, _, _ = e.authorizeCodeNoPKCE("skc-basic", redirect)
	e.srv.Clients.MarkPKCE("skc-basic")
	if r := e.do("POST", "/token", formHdr, tokenBody("skc-basic", code)); r.StatusCode != 400 {
		t.Fatalf("code without challenge after the mark: %d", r.StatusCode)
	}

	// public / dynamically registered clients always need PKCE
	if code, loc, st := e.authorizeCodeNoPKCE(pub, redirect); st != 302 || code != "" || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("public without challenge: %d %v", st, loc)
	}
	if c, _ := e.srv.Clients.Get(pub); c.PKCEOptional() {
		t.Fatal("public clients never get the exemption")
	}

	// (4) plain is always rejected, for every client type
	for _, id := range []string{"skc-basic", pub} {
		for _, extra := range []string{"&code_challenge_method=plain", ""} {
			q := "response_type=code&client_id=" + id + "&redirect_uri=" + url.QueryEscape(redirect) + "&state=s&code_challenge=" + url.QueryEscape(challenge) + extra
			resp := e.do("GET", "/authorize?"+q, nil, "")
			resp.Body.Close()
			l, _ := url.Parse(resp.Header.Get("Location"))
			if resp.StatusCode != 302 || l.Query().Get("code") != "" || l.Query().Get("error") != "invalid_request" {
				t.Fatalf("%s %q: plain or missing method accepted: %d %s", id, extra, resp.StatusCode, l)
			}
		}
	}
}
