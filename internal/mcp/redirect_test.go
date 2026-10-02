package mcp

import (
	"strings"
	"testing"
)

// There is no origin allowlist: any https origin is fine, plain http only on loopback, and the
// syntax rules still apply.
func TestRedirectAllowed(t *testing.T) {
	cases := []struct {
		uri  string
		want bool
	}{
		{"https://hosted.example/r/abc123", true},
		{"https://anything.example/cb", true},
		{"https://a.b.c.example:8443/x?y=1", true},
		{"https://HOSTED.example:443/r/x", true},
		{"http://127.0.0.1:53211/callback", true},
		{"http://localhost:3000/cb", true},
		{"http://[::1]:8080/cb", true},
		{"http://127.0.0.1/cb", true},
		// rejections
		{"http://lan.example/cb", false}, // plain http off loopback
		{"http://hosted.example/r/x", false},
		{"https://hosted.example@evil.com/", false},
		{"https://user:pw@hosted.example/r/x", false},
		{"https://hosted.example/r/x#frag", false},
		{"https://hosted.example\\@evil.com/", false},
		{"https://anything.example/a b", false},
		{"javascript:alert(1)", false},
		{"data:text/html,hi", false},
		{"ftp://hosted.example/x", false},
		{"//hosted.example/r/x", false},
		{"/relative", false},
		{"", false},
		{"http://127.0.0.1.evil.com/cb", false},
		{"http://localhost.evil.com/cb", false},
		{"http://0.0.0.0:80/cb", false},
		{"myapp://callback", false},
	}
	for _, c := range cases {
		if got := RedirectAllowed(c.uri); got != c.want {
			t.Errorf("RedirectAllowed(%q) = %v, want %v", c.uri, got, c.want)
		}
	}
	if why, ok := RedirectCheck("http://lan.example/cb"); ok || !strings.Contains(why, "plain http") {
		t.Errorf("reason: %q", why)
	}
}

// Any https origin may register, but /authorize still requires the exact registered redirect.
func TestAnyOriginStillRequiresExactRegisteredRedirect(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register("https://random.example/cb", "none")
	id := reg["client_id"].(string)
	_, challenge := pkce()
	if code, _, st := e.authorizeCode(id, "https://random.example/cb", challenge, ""); st != 302 || code == "" {
		t.Fatalf("registered redirect: %d", st)
	}
	if _, _, st := e.authorizeCode(id, "https://random.example/other", challenge, ""); st != 400 {
		t.Fatalf("different path must fail: %d", st)
	}
	if _, _, st := e.authorizeCode(id, "https://other.example/cb", challenge, ""); st != 400 {
		t.Fatalf("different origin must fail: %d", st)
	}
	for _, bad := range []string{`{"redirect_uris":["http://lan.example/cb"]}`, `{"redirect_uris":["https://u:p@x.example/cb"]}`, `{"redirect_uris":["https://x.example/cb#f"]}`} {
		r := e.do("POST", "/register", map[string]string{"Content-Type": "application/json"}, bad)
		if r.StatusCode != 400 {
			t.Errorf("accepted %s: %d", bad, r.StatusCode)
		}
	}
	if reg["client_secret"] != nil || reg["token_endpoint_auth_method"] != "none" {
		t.Errorf("public client expected: %v", reg)
	}
}
