package mcp

import (
	"strings"
	"testing"
)

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

func TestImportAndTesterFollowTheAddressRule(t *testing.T) {
	items, err := ParseImport(`{"mcpServers":{"lan":{"url":"http://mealie:9000/mcp"},"web":{"url":"http://mcp.example.com/mcp"},"ok":{"url":"https://mcp.example.com/mcp"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.Upstream.Alias] = it.Err
	}
	if got["lan"] != "" || got["ok"] != "" || !strings.Contains(got["web"], "https") {
		t.Errorf("import errors: %v", got)
	}
	// a stored upstream from before the rule is not tested over plain http on the internet
	e := newEnv(t, nil)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "old", URL: "http://127.0.0.1:1/mcp", AuthKind: AuthNone, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.srv.Upstreams.db.Exec(`UPDATE upstreams SET url = 'http://mcp.example.com/mcp' WHERE alias = 'old'`); err != nil {
		t.Fatal(err)
	}
	if res := e.srv.Test(t.Context(), "old"); res.OK || !strings.Contains(res.Error, "https") {
		t.Errorf("test: %+v", res)
	}
}
