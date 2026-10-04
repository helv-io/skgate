package mcp

import "testing"

func TestAddressSchemeRule(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
	}{
		{"http://mealie:9000", true}, {"http://nas.local/api", true}, {"http://10.0.0.7:8080", true},
		{"https://api.example.com", true}, {"http://api.example.com", false}, {"http://example.org:8080/v1", false},
	} {
		oa := Upstream{Alias: "a", Kind: KindOpenAPI, URL: tc.url, AuthKind: AuthNone, Enabled: true}
		if err := oa.Validate(); (err == nil) != tc.ok {
			t.Errorf("openapi %s: %v", tc.url, err)
		}
		rm := Upstream{Alias: "a", Kind: KindRemote, URL: tc.url, AuthKind: AuthNone, Enabled: true}
		if err := rm.Validate(); (err == nil) != tc.ok {
			t.Errorf("remote %s: %v", tc.url, err)
		}
	}
}
