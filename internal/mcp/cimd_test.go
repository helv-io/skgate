package mcp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseMetadataClientID(t *testing.T) {
	for _, ok := range []string{"https://app.example/client.json", "https://app.example:8443/oauth/client-metadata.json", "https://app.example/c?v=1", "HTTPS://app.example/x"} {
		if _, why := ParseMetadataClientID(ok); why != "" {
			t.Errorf("%s refused: %s", ok, why)
		}
	}
	for _, bad := range []string{
		"", "app.example/client.json", "http://app.example/client.json", "ftp://app.example/c", "https://", "https:///c.json",
		"https://app.example", "https://app.example/", "https://u:p@app.example/c.json", "https://app.example/c.json#frag",
		"https://app.example/a/../c.json", "https://app.example/./c.json", "https://app.example/c json", "https://app.example/c\\x",
		"https://app.example/" + strings.Repeat("a", 2100), "skc-abcdef", "https:app.example/c.json",
	} {
		if _, why := ParseMetadataClientID(bad); why == "" {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPublicIPs(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "93.184.216.34", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888", "64:ff9b::808:808", "2002:808:808::1", "::ffff:8.8.8.8"} {
		if !isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range []string{
		"127.0.0.1", "127.1.2.3", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "0.0.0.0", "100.64.0.1", "100.127.255.255",
		"192.0.0.8", "192.0.2.1", "198.18.0.1", "198.51.100.7", "203.0.113.9", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "2001:db8::1", "2001::1", "64:ff9b::a00:1", "64:ff9b::7f00:1", "2002:a00:1::1", "2002:7f00:1::1",
		"::ffff:127.0.0.1", "::ffff:10.1.2.3", "::ffff:169.254.169.254", "100::1",
	} {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not be public", s)
		}
	}
	if isPublicIP(nil) {
		t.Error("nil")
	}
}

func TestPublicDialerChecksTheAddressItConnectsTo(t *testing.T) {
	var dialed []string
	lookups := 0
	p := publicDialer{
		lookup: func(context.Context, string) ([]net.IP, error) {
			lookups++
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
		dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			return nil, errors.New("stop")
		},
	}
	if _, err := p.DialContext(t.Context(), "tcp", "app.example:443"); err == nil || err.Error() != "stop" {
		t.Fatalf("%v", err)
	}
	if lookups != 1 || len(dialed) != 1 || dialed[0] != "93.184.216.34:443" {
		t.Errorf("it must connect to the address it checked, once resolved: lookups=%d dialed=%v", lookups, dialed)
	}

	// one private answer among public ones refuses the whole name; nothing is dialed
	for name, ips := range map[string][]string{
		"mixed":    {"93.184.216.34", "10.0.0.5"},
		"loopback": {"127.0.0.1"},
		"metadata": {"169.254.169.254"},
		"v6 local": {"::1"},
		"mapped":   {"::ffff:192.168.0.1"},
	} {
		dialed = nil
		var parsed []net.IP
		for _, s := range ips {
			parsed = append(parsed, net.ParseIP(s))
		}
		q := p
		q.lookup = func(context.Context, string) ([]net.IP, error) { return parsed, nil }
		if _, err := q.DialContext(t.Context(), "tcp", "evil.example:443"); !errors.Is(err, errNotPublic) || len(dialed) != 0 {
			t.Errorf("%s: err=%v dialed=%v", name, err, dialed)
		}
	}
	// IP literals are checked too, without a lookup
	lookups = 0
	for _, a := range []string{"127.0.0.1:443", "[::1]:443", "10.0.0.1:80", "169.254.169.254:80"} {
		if _, err := p.DialContext(t.Context(), "tcp", a); !errors.Is(err, errNotPublic) {
			t.Errorf("%s: %v", a, err)
		}
	}
	if lookups != 0 {
		t.Error("literals need no lookup")
	}
	// the system dialer refuses localhost for real
	if _, err := systemDialer().DialContext(t.Context(), "tcp", "localhost:443"); !errors.Is(err, errNotPublic) {
		t.Errorf("localhost: %v", err)
	}
}

func TestParseMetadataDocument(t *testing.T) {
	const id = "https://app.example/client.json"
	u, _ := ParseMetadataClientID(id)
	good := `{"client_id":"` + id + `","client_name":"  App\u0007 Name ","redirect_uris":["cursor://x/cb","https://app.example/cb","http://127.0.0.1:7/cb","http://lan.example/cb"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token","implicit"],"response_types":["code"],"extra":1}`
	c, err := parseMetadata(id, u, []byte(good))
	if err != nil || c.Name != "App Name" || c.Source != cimdSource || c.AuthMethod != "none" || c.ID != id || len(c.RedirectURIs) != 2 || c.Confidential() {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err := parseMetadata(id, u, []byte(`{"client_id":"`+id+`","redirect_uris":["https://app.example/cb"]}`)); err != nil || c.Name != "app.example" {
		t.Errorf("a missing name falls back to the host: %+v %v", c, err)
	}
	chatgpt := `{"client_id":"` + id + `","client_name":"ChatGPT","redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["none","private_key_jwt"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_signing_alg":"RS256","jwks_uri":"https://chatgpt.com/oauth/jwks.json"}`
	if c, err := parseMetadata(id, u, []byte(chatgpt)); err != nil || c.AuthMethod != "private_key_jwt" || c.JWKSURI != "https://chatgpt.com/oauth/jwks.json" || c.Confidential() {
		t.Fatalf("ChatGPT-shaped document: %+v %v", c, err)
	}
	for name, doc := range map[string]string{
		"not json":       `nope`,
		"array":          `[]`,
		"other id":       `{"client_id":"https://other.example/client.json","redirect_uris":["https://app.example/cb"]}`,
		"no id":          `{"redirect_uris":["https://app.example/cb"]}`,
		"secret":         `{"client_id":"` + id + `","redirect_uris":["https://app.example/cb"],"client_secret":"s"}`,
		"secret expiry":  `{"client_id":"` + id + `","redirect_uris":["https://app.example/cb"],"client_secret_expires_at":0}`,
		"basic auth":     `{"client_id":"` + id + `","redirect_uris":["https://app.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		"private_key no jwks": `{"client_id":"` + id + `","redirect_uris":["https://app.example/cb"],"token_endpoint_auth_method":"private_key_jwt"}`,
		"bad alg": `{"client_id":"` + id + `","redirect_uris":["https://app.example/cb"],"token_endpoint_auth_method":"private_key_jwt","jwks_uri":"https://app.example/jwks.json","token_endpoint_auth_signing_alg":"HS256"}`,
		"no redirects":   `{"client_id":"` + id + `"}`,
		"only unusable":  `{"client_id":"` + id + `","redirect_uris":["cursor://x/cb","http://lan.example/cb"]}`,
		"too many":       `{"client_id":"` + id + `","redirect_uris":["https://a.example/1","https://a.example/2","https://a.example/3","https://a.example/4","https://a.example/5","https://a.example/6","https://a.example/7","https://a.example/8","https://a.example/9","https://a.example/10","https://a.example/11"]}`,
		"other grants":   `{"client_id":"` + id + `","redirect_uris":["https://app.example/cb"],"grant_types":["client_credentials"]}`,
		"other response": `{"client_id":"` + id + `","redirect_uris":["https://app.example/cb"],"response_types":["token"]}`,
	} {
		if c, err := parseMetadata(id, u, []byte(doc)); err == nil {
			t.Errorf("%s accepted: %+v", name, c)
		}
	}
}

func TestCacheTTL(t *testing.T) {
	for cc, want := range map[string]time.Duration{
		"":                           cimdDefaultTTL,
		"public, max-age=7200":       2 * time.Hour,
		"max-age=10":                 cimdMinTTL,
		"max-age=99999999":           cimdMaxTTL,
		"no-store":                   cimdMinTTL,
		"max-age=7200, no-cache":     cimdMinTTL,
		"max-age=abc":                cimdDefaultTTL,
		`max-age="3600"`:             time.Hour,
		"private, MAX-AGE=1800":      30 * time.Minute,
		"s-maxage=60, max-age=86400": 24 * time.Hour,
	} {
		if got := cacheTTL(cc); got != want {
			t.Errorf("%q: %v, want %v", cc, got, want)
		}
	}
}
