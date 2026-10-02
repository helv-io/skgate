package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestKeysPageFooterAndCodeNoWrap(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	_, page := br.get("/admin/keys")
	for _, want := range []string{
		"<li>OpenAI API: <code>" + ts.URL + "/v1</code></li>",
		"<li>MCP: <code>" + ts.URL + "/mcp/&lt;alias&gt;</code></li>",
		"X-API-Key",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("keys footer missing %q", want)
		}
	}
	r, _ := http.Get(ts.URL + "/admin/static/app.css")
	css, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(css), "white-space:nowrap") {
		t.Error("code snippets must not wrap")
	}
}

func TestNoProviderSpecificWording(t *testing.T) {
	_, _, br, _ := signedIn(t, nil)
	for _, p := range []string{"/admin/upstreams", "/admin/clients", "/admin/keys"} {
		_, page := br.get(p)
		for _, bad := range []string{"Gemini", "gemini", "googleusercontent", "Google"} {
			if strings.Contains(page, bad) {
				t.Errorf("%s contains provider-specific %q", p, bad)
			}
		}
	}
}
