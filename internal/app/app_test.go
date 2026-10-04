package app

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/oidctest"
	"github.com/helv-io/skgate/internal/store"
)

func newApp(t *testing.T, mut func(*config.Config, *oidctest.Provider)) (*App, *httptest.Server, *oidctest.Provider) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	idp := oidctest.New(t)
	cfg := &config.Config{PublicURL: "http://x", OIDCIssuer: idp.URL, OIDCClientID: idp.ClientID,
		OIDCClientSecret: idp.ClientSecret, OIDCScopes: "openid profile email groups"}
	if mut != nil {
		mut(cfg, idp)
	}
	// the handler reads cfg.PublicURL per request, so the real address can be set after start
	mux := &swapHandler{}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	cfg.PublicURL = ts.URL
	a := New(cfg, db)
	mux.h = a.Handler()
	return a, ts, idp
}

type swapHandler struct{ h http.Handler }

func (s *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.h.ServeHTTP(w, r) }

// browser is a tiny cookie-keeping client that never follows redirects on its own.
type browser struct {
	t  *testing.T
	c  *http.Client
	ts *httptest.Server
}

func newBrowser(t *testing.T, ts *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, ts: ts, c: &http.Client{Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) do(req *http.Request) (*http.Response, string) {
	r, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	return r, string(body)
}

func (b *browser) get(path string) (*http.Response, string) {
	if !strings.HasPrefix(path, "http") {
		path = b.ts.URL + path
	}
	req, _ := http.NewRequest("GET", path, nil)
	return b.do(req)
}

func (b *browser) post(path string, v url.Values) (*http.Response, string) {
	if path == "/admin/upstreams/save" && v.Get("mode") == "edit" { // an upstream is addressed by its alias
		path = "/admin/upstreams/" + v.Get("alias") + "/save"
	}
	req, _ := http.NewRequest("POST", b.ts.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, body := b.do(req)
	// a change that has something to show (a new key, an import's outcome) redirects to its result screen: follow it
	if loc := resp.Header.Get("Location"); resp.StatusCode == http.StatusSeeOther && strings.HasPrefix(loc, "/admin/results/") {
		return b.get(loc)
	}
	return resp, body
}

// sso walks the full OIDC flow: login, IdP authorization, callback. It returns the callback response.
func (b *browser) sso(idp *oidctest.Provider, next string) (*http.Response, string) {
	r, _ := b.get("/admin/oidc/login?next=" + url.QueryEscape(next))
	if r.StatusCode != 302 {
		b.t.Fatalf("oidc login: %d", r.StatusCode)
	}
	cbURL := idp.Authorize(b.t, r.Header.Get("Location"))
	return b.get(cbURL)
}

func TestHealthzAndAdminFlow(t *testing.T) {
	a, ts, idp := newApp(t, nil)
	a.Admin.NoSaveTest = true // stores addresses nothing answers at
	resp, _ := http.Get(ts.URL + "/healthz")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "ok" {
		t.Fatalf("healthz %d %q", resp.StatusCode, b)
	}
	for _, f := range []string{"/admin/static/app.css", "/admin/static/app.js"} {
		r, _ := http.Get(ts.URL + f)
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", f, r.StatusCode)
		}
	}
	br := newBrowser(t, ts)
	get, post := br.get, br.post
	if r, _ := get("/admin"); r.StatusCode != 302 || r.Header.Get("Location") != "/admin/oidc/login?next=%2Fadmin" {
		t.Fatalf("unauthenticated admin should go straight to the OIDC login start: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, _ := get("/admin/keys?x=1"); r.StatusCode != 302 || r.Header.Get("Location") != "/admin/oidc/login?next=%2Fadmin%2Fkeys%3Fx%3D1" {
		t.Fatalf("return path must be preserved: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, _ := get("/admin/login?next=//evil.example"); r.StatusCode != 302 || r.Header.Get("Location") != "/admin/oidc/login?next=%2Fadmin" {
		t.Fatalf("legacy login must redirect into OIDC with a safe next: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	for _, path := range []string{"/admin/setup"} {
		if r, _ := get(path); r.StatusCode != 404 {
			t.Fatalf("%s must not exist: %d", path, r.StatusCode)
		}
	}
	if r, _ := post("/admin/login", url.Values{"password": {"anything"}}); r.StatusCode == 303 {
		t.Fatal("password login must be gone")
	}
	r, _ := br.sso(idp, "/admin")
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin" {
		t.Fatalf("sso callback: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	var sc string
	for _, c := range r.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "skgate_session=") && !strings.Contains(c, "Max-Age=0") {
			sc = c
		}
	}
	if !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Lax") {
		t.Fatalf("cookie flags: %q", sc)
	}
	_, page := get("/admin")
	if !strings.Contains(page, "admin@example.com") {
		t.Fatal("signed-in user should be shown")
	}
	if !strings.Contains(page, "<h3>Grok</h3>") || strings.Contains(page, "access_token") {
		t.Fatal("status page wrong")
	}
	csrf := between(page, `name="csrf" value="`, `"`)
	if csrf == "" {
		t.Fatal("no csrf token in page")
	}
	// POST without CSRF is refused
	if r, _ := post("/admin/keys/create", url.Values{"label": {"x"}}); r.StatusCode != 403 {
		t.Fatalf("missing csrf: %d", r.StatusCode)
	}
	r, body := post("/admin/keys/create", url.Values{"label": {"ha"}, "csrf": {csrf}})
	key := between(body, `class="copybox" data-copy-text="`, `"`)
	if r.StatusCode != 200 || !strings.HasPrefix(key, "sk-") || len(key) != 51 {
		t.Fatalf("key create: %d %q", r.StatusCode, key)
	}
	_, list := get("/admin/keys")
	if strings.Contains(list, key) || strings.Contains(list, key[:8]) || !strings.Contains(list, "************"+key[len(key)-4:]) {
		t.Fatal("full key and its prefix must not be shown again, only asterisks plus the last 4 characters")
	}
	// upstream with secret: value is never rendered
	r, _ = post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"demo"}, "url": {"http://demo:8000/mcp"},
		"auth_kind": {"bearer"}, "auth_value": {"SUPER-SECRET-VALUE"}, "enabled": {"1"}, "include": {"1"}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/upstreams" {
		t.Fatalf("upstream save: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if k, _ := flashOf(r); k != "ok" {
		t.Fatalf("upstream save should queue a success toast, got %q", k)
	}
	_, ups := get("/admin/upstreams")
	_, edit := get("/admin/upstreams/demo/edit")
	if !strings.Contains(ups, "************"+"ALUE") || !strings.Contains(edit, "************"+"ALUE") || strings.Contains(ups+edit, "SECRET-VAL") {
		t.Fatal("upstream credential must render as asterisks plus the last 4 characters only")
	}
	if strings.Contains(ups+edit, "SUPER-SECRET-VALUE") || !strings.Contains(ups, "/mcp/demo") || !strings.Contains(ups, "DCR clients: leave client ID and secret empty") {
		t.Fatal("secret rendered or connection notes missing")
	}
	// editing with empty secret keeps it
	post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"edit"}, "alias": {"demo"}, "url": {"http://demo:8000/mcp2"}, "auth_kind": {"bearer"}, "enabled": {"1"}})
	if u, _ := a.MCP.Upstreams.Get("demo"); u.AuthValue != "SUPER-SECRET-VALUE" || u.URL != "http://demo:8000/mcp2" {
		t.Fatalf("edit lost the secret: %+v", u)
	}
	// manual OAuth client
	r, body = post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"manual"}, "redirects": {"https://client.example/cb"}, "method": {"client_secret_post"}})
	if r.StatusCode != 200 || !strings.Contains(body, "skc-") || !strings.Contains(body, `aria-label="Copy the client secret"`) {
		t.Fatalf("client create: %d", r.StatusCode)
	}
	if r, _ := post("/admin/clients/create", url.Values{"csrf": {csrf}, "redirects": {"http://evil.example/cb"}, "method": {"none"}}); r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Fatal("insecure redirect accepted for manual client")
	}
	// the new virtual key works on /v1 auth (401 -> 503 shows auth passed, no Grok login)
	req, _ := http.NewRequest("GET", ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("expected 503 (not signed in to Grok), got %d", resp.StatusCode)
	}
}

func TestOIDCNotConfiguredClosesAdmin(t *testing.T) {
	_, ts, _ := newApp(t, func(c *config.Config, _ *oidctest.Provider) {
		c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret = "", "", ""
	})
	br := newBrowser(t, ts)
	for _, path := range []string{"/admin", "/admin/keys", "/admin/login", "/admin/upstreams", "/admin/logout", "/admin/signed-out", "/admin/oidc/login", "/admin/oidc/callback?code=x&state=y"} {
		r, body := br.get(path)
		if r.StatusCode != 503 || !strings.Contains(body, "OIDC not configured") {
			t.Fatalf("%s: want 503 OIDC not configured page, got %d", path, r.StatusCode)
		}
	}
	// a forged-looking cookie must not open anything either
	req, _ := http.NewRequest("POST", ts.URL+"/admin/keys/create", strings.NewReader("label=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "skgate_session", Value: "9999999999.aaa.bbb"})
	if r, _ := br.do(req); r.StatusCode != 503 {
		t.Fatalf("unconfigured admin POST: %d", r.StatusCode)
	}
	// partial config is still "not configured"
	_, ts2, _ := newApp(t, func(c *config.Config, _ *oidctest.Provider) { c.OIDCClientSecret = "" })
	if r, body := newBrowser(t, ts2).get("/admin"); r.StatusCode != 503 || !strings.Contains(body, "OIDC not configured") {
		t.Fatal("missing client secret must count as not configured")
	}
}

func TestAdminSSOEnforcesAllowList(t *testing.T) {
	_, ts, idp := newApp(t, func(c *config.Config, _ *oidctest.Provider) { c.OIDCEmails = []string{"boss@example.com"} })
	br := newBrowser(t, ts)
	r, body := br.sso(idp, "/admin")
	if r.StatusCode != 403 || !strings.Contains(body, "not allowed") {
		t.Fatalf("disallowed user: %d", r.StatusCode)
	}
	if strings.Contains(body, "<form") || r.Header.Get("Location") != "" {
		t.Fatal("error page must be plain, no redirect")
	}
	if r, _ := br.get("/admin"); r.StatusCode != 302 {
		t.Fatalf("no session must exist after rejection: %d", r.StatusCode)
	}
}

func TestAdminSSOBadTokenGivesNoSession(t *testing.T) {
	_, ts, idp := newApp(t, nil)
	idp.SignWithOther = true
	br := newBrowser(t, ts)
	if r, _ := br.sso(idp, "/admin"); r.StatusCode != 401 {
		t.Fatalf("bad signature: %d", r.StatusCode)
	}
	if r, _ := br.get("/admin"); r.StatusCode != 302 {
		t.Fatalf("no session expected: %d", r.StatusCode)
	}
}

func TestLogoutRedirectsToEndSession(t *testing.T) {
	_, ts, idp := newApp(t, nil)
	br := newBrowser(t, ts)
	br.sso(idp, "/admin")
	_, page := br.get("/admin")
	csrf := between(page, `name="csrf" value="`, `"`)
	r, _ := br.post("/admin/logout", url.Values{"csrf": {csrf}})
	if loc := r.Header.Get("Location"); r.StatusCode != 303 || !strings.HasPrefix(loc, idp.URL+"/x/logout?") {
		t.Fatalf("logout should go to discovered end_session_endpoint: %d %s", r.StatusCode, loc)
	}
	if r, _ := br.get("/admin"); r.StatusCode != 302 {
		t.Fatal("session must be cleared after logout")
	}
}

func TestLogoutShowsSignedOutPageWithoutLoop(t *testing.T) {
	_, ts, idp := newApp(t, func(c *config.Config, p *oidctest.Provider) { p.NoEndSession = true })
	br := newBrowser(t, ts)
	br.sso(idp, "/admin")
	_, page := br.get("/admin")
	csrf := between(page, `name="csrf" value="`, `"`)
	r, _ := br.post("/admin/logout", url.Values{"csrf": {csrf}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/signed-out" {
		t.Fatalf("logout without end_session_endpoint: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	r, body := br.get("/admin/signed-out")
	if r.StatusCode != 200 || r.Header.Get("Location") != "" || !strings.Contains(body, "Signed out") || !strings.Contains(body, `href="/admin/oidc/login"`) {
		t.Fatalf("signed-out page must be static with a Sign in link: %d", r.StatusCode)
	}
	// logging out again without a session lands on the same page, not on a redirect chain to the IdP
	if r, _ := br.post("/admin/logout", url.Values{}); r.StatusCode != 303 || r.Header.Get("Location") != "/admin/signed-out" {
		t.Fatalf("logout without session: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, _ := br.get("/admin"); r.StatusCode != 302 {
		t.Fatal("session must be cleared after logout")
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}
