package oidcauth

import (
	"context"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/oidctest"
)

const cb = "https://skgate.example/admin/oidc/callback"

func newClient(p *oidctest.Provider, mut func(*Config)) *Client {
	cfg := Config{Issuer: p.URL, ClientID: p.ClientID, ClientSecret: p.ClientSecret}
	if mut != nil {
		mut(&cfg)
	}
	c := New(cfg, func() []byte { return []byte("0123456789abcdef0123456789abcdef") },
		func() string { return cb }, func() bool { return true })
	return c
}

// begin runs the first leg and returns the flow cookie and the IdP callback URL.
func begin(t *testing.T, c *Client, p *oidctest.Provider) (*http.Cookie, string, url.Values) {
	t.Helper()
	w := httptest.NewRecorder()
	if err := c.Begin(w, httptest.NewRequest("GET", "/admin/oidc/login", nil), "/admin/keys"); err != nil {
		t.Fatal(err)
	}
	res := w.Result()
	if res.StatusCode != 302 {
		t.Fatalf("begin: %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, p.URL+"/x/authz?") {
		t.Fatalf("must redirect to the discovered authorization endpoint, got %s", loc)
	}
	u, _ := url.Parse(loc)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" ||
		q.Get("state") == "" || q.Get("scope") != DefaultScopes || q.Get("redirect_uri") != cb {
		t.Fatalf("bad authorization request: %v", q)
	}
	ck := res.Cookies()
	if len(ck) != 1 || !ck[0].HttpOnly || !ck[0].Secure || ck[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("flow cookie attributes: %+v", ck)
	}
	return ck[0], loc, q
}

func finish(c *Client, ck *http.Cookie, callback string) (Identity, string, error) {
	req := httptest.NewRequest("GET", callback, nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	return c.Finish(httptest.NewRecorder(), req)
}

func TestHappyPathUsesDiscoveryOnly(t *testing.T) {
	p := oidctest.New(t)
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	id, next, err := finish(c, ck, p.Authorize(t, authURL))
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "admin@example.com" || id.Subject != "u-1" || next != "/admin/keys" || id.IDToken == "" {
		t.Fatalf("identity %+v next %q", id, next)
	}
	for _, path := range p.Paths() {
		switch path {
		case "/.well-known/openid-configuration", "/x/authz", "/x/token", "/x/keys", "/x/userinfo":
		default:
			t.Fatalf("unexpected request path %q: endpoints must come from discovery", path)
		}
	}
	if !p.BasicAuthSeen || p.PostAuthSeen {
		t.Fatal("client_secret_basic must be preferred")
	}
	// discovery is cached: a second flow does not refetch it
	n := 0
	for _, path := range p.Paths() {
		if path == "/.well-known/openid-configuration" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("discovery fetched %d times", n)
	}
}

func TestClientSecretPostFallback(t *testing.T) {
	p := oidctest.New(t)
	p.OnlyPost = true
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err != nil {
		t.Fatal(err)
	}
	if !p.PostAuthSeen {
		t.Fatal("should fall back to client_secret_post")
	}
}

// Without advertised methods basic is tried first (the specification's default); a token endpoint
// that refuses it with invalid_client gets one retry with the secret in the form, and the choice is
// remembered so later logins do not fail first.
func TestClientAuthDefaultsToBasicThenRetriesPost(t *testing.T) {
	p := oidctest.New(t)
	p.HideMethods = true
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err != nil || p.BasicHits != 1 || p.PostHits != 0 {
		t.Fatalf("default: %v basic=%d post=%d", err, p.BasicHits, p.PostHits)
	}

	p = oidctest.New(t)
	p.HideMethods, p.OnlyPost = true, true
	c = newClient(p, nil)
	ck, authURL, _ = begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err != nil || p.BasicHits != 1 || p.PostHits != 1 {
		t.Fatalf("retry: %v basic=%d post=%d", err, p.BasicHits, p.PostHits)
	}
	ck, authURL, _ = begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err != nil || p.BasicHits != 1 || p.PostHits != 2 {
		t.Fatalf("remembered: %v basic=%d post=%d", err, p.BasicHits, p.PostHits)
	}

	// a wrong secret fails after the single retry, and says which methods were tried
	p = oidctest.New(t)
	p.HideMethods = true
	c = newClient(p, func(cfg *Config) { cfg.ClientSecret = "wrong" })
	ck, authURL, _ = begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err == nil || p.BasicHits != 1 || p.PostHits != 1 {
		t.Fatalf("wrong secret: %v basic=%d post=%d", err, p.BasicHits, p.PostHits)
	}
}

func TestAuthStyleChoice(t *testing.T) {
	for _, tc := range []struct {
		methods  []string
		postOnly bool
		want     oauth2.AuthStyle
	}{
		{nil, false, oauth2.AuthStyleInHeader},
		{[]string{"client_secret_post", "client_secret_basic"}, false, oauth2.AuthStyleInHeader},
		{[]string{"client_secret_post"}, false, oauth2.AuthStyleInParams},
		{[]string{"private_key_jwt"}, false, oauth2.AuthStyleInHeader},
		{nil, true, oauth2.AuthStyleInParams},
	} {
		if got, _ := authStyle(tc.methods, tc.postOnly); got != tc.want {
			t.Errorf("%v postOnly=%v: %v", tc.methods, tc.postOnly, got)
		}
	}
}

func TestStateMismatch(t *testing.T) {
	p := oidctest.New(t)
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	cbURL, _ := url.Parse(p.Authorize(t, authURL))
	q := cbURL.Query()
	q.Set("state", "forged")
	cbURL.RawQuery = q.Encode()
	if _, _, err := finish(c, ck, cbURL.String()); err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("want state mismatch, got %v", err)
	}
}

func TestMissingOrTamperedFlowCookie(t *testing.T) {
	p := oidctest.New(t)
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	cbURL := p.Authorize(t, authURL)
	if _, _, err := finish(c, nil, cbURL); err == nil {
		t.Fatal("missing cookie must fail")
	}
	bad := *ck
	bad.Value = ck.Value[:len(ck.Value)-3] + "AAA"
	if _, _, err := finish(c, &bad, cbURL); err == nil {
		t.Fatal("tampered cookie must fail")
	}
}

func TestNonceMismatch(t *testing.T) {
	p := oidctest.New(t)
	p.NonceOverride = "attacker-nonce"
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("want nonce mismatch, got %v", err)
	}
}

func TestBadSignatureRejected(t *testing.T) {
	p := oidctest.New(t)
	p.SignWithOther = true
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err == nil {
		t.Fatal("ID token signed with an unknown key must be rejected")
	}
}

func TestWrongAudienceRejected(t *testing.T) {
	p := oidctest.New(t)
	p.Aud = "some-other-client"
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err == nil {
		t.Fatal("wrong aud must be rejected")
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	p := oidctest.New(t)
	p.Expired = true
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err == nil {
		t.Fatal("expired ID token must be rejected")
	}
}

func TestIssuerMismatchRejected(t *testing.T) {
	p := oidctest.New(t)
	p.IssuerOverride = "https://evil.example"
	c := newClient(p, nil)
	if err := c.Begin(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "/"); err == nil {
		t.Fatal("discovered issuer that differs from the configured one must be rejected")
	}
}

func TestWrongClientSecret(t *testing.T) {
	p := oidctest.New(t)
	c := newClient(p, func(c *Config) { c.ClientSecret = "wrong" })
	ck, authURL, _ := begin(t, c, p)
	if _, _, err := finish(c, ck, p.Authorize(t, authURL)); err == nil {
		t.Fatal("token exchange with a wrong secret must fail")
	}
}

func run(t *testing.T, p *oidctest.Provider, mut func(*Config)) error {
	t.Helper()
	c := newClient(p, mut)
	ck, authURL, _ := begin(t, c, p)
	_, _, err := finish(c, ck, p.Authorize(t, authURL))
	return err
}

func TestAllowedEmails(t *testing.T) {
	p := oidctest.New(t)
	if err := run(t, p, func(c *Config) { c.AllowedEmails = []string{"ADMIN@example.com"} }); err != nil {
		t.Fatalf("case-insensitive email allow: %v", err)
	}
	if err := run(t, p, func(c *Config) { c.AllowedEmails = []string{"other@example.com"} }); err != ErrForbidden {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	no := false
	p.EmailVerified = &no
	if err := run(t, p, func(c *Config) { c.AllowedEmails = []string{"admin@example.com"} }); err != ErrForbidden {
		t.Fatalf("unverified email must not match, got %v", err)
	}
}

func TestAllowedGroups(t *testing.T) {
	p := oidctest.New(t)
	if err := run(t, p, func(c *Config) { c.AllowedGroups = []string{"users", "admins"} }); err != nil {
		t.Fatalf("group allow: %v", err)
	}
	if err := run(t, p, func(c *Config) { c.AllowedGroups = []string{"nope"} }); err != ErrForbidden {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	p.Groups = nil
	if err := run(t, p, func(c *Config) { c.AllowedGroups = []string{"admins"} }); err != ErrForbidden {
		t.Fatalf("no groups claim must be forbidden, got %v", err)
	}
}

func TestEmailOrGroupEitherMatches(t *testing.T) {
	p := oidctest.New(t)
	if err := run(t, p, func(c *Config) {
		c.AllowedEmails = []string{"x@example.com"}
		c.AllowedGroups = []string{"admins"}
	}); err != nil {
		t.Fatalf("group match should suffice: %v", err)
	}
}

func TestNoListsAllowsAnyAuthenticatedUser(t *testing.T) {
	p := oidctest.New(t)
	p.Email, p.Groups = "", nil
	if err := run(t, p, nil); err != nil {
		t.Fatal(err)
	}
}

func TestClaimsFromUserinfo(t *testing.T) {
	p := oidctest.New(t)
	p.InfoOnly = true
	if err := run(t, p, func(c *Config) { c.AllowedGroups = []string{"admins"} }); err != nil {
		t.Fatalf("groups from userinfo should be honoured: %v", err)
	}
}

func TestLazyDiscoveryWithRetry(t *testing.T) {
	p := oidctest.New(t)
	p.SetDown(true)
	c := newClient(p, nil) // construction must not touch the network
	if len(p.Paths()) != 0 {
		t.Fatal("New must not fetch discovery")
	}
	if err := c.Begin(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "/"); err == nil {
		t.Fatal("expected error while IdP is down")
	}
	p.SetDown(false)
	time.Sleep(discoveryRetry + 100*time.Millisecond)
	begin(t, c, p) // recovers on its own once the IdP is back
}

func TestEndSessionURL(t *testing.T) {
	p := oidctest.New(t)
	c := newClient(p, nil)
	ck, authURL, _ := begin(t, c, p)
	id, _, err := finish(c, ck, p.Authorize(t, authURL))
	if err != nil {
		t.Fatal(err)
	}
	u := c.EndSessionURL(context.Background(), id.IDToken, "https://skgate.example/admin/login")
	if !strings.HasPrefix(u, p.URL+"/x/logout?") || !strings.Contains(u, "id_token_hint=") || !strings.Contains(u, "client_id=skgate") {
		t.Fatalf("end session url %q", u)
	}
	p2 := oidctest.New(t)
	p2.NoEndSession = true
	if got := newClient(p2, nil).EndSessionURL(context.Background(), "x", ""); got != "" {
		t.Fatalf("no end_session_endpoint should give empty url, got %q", got)
	}
}
