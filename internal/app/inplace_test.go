package app

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/mcp"
)

// One-click actions on every admin page save in place: the Upstreams switches and Delete, the process actions, a
// key's Save and Revoke, a client's Delete and Delete unused, and Add and Remove provider. A browser scrolls down,
// acts, and checks there was no navigation, the scroll stayed, the page shows the new state and the server has it.
func TestOneClickActionsSaveInPlaceInBrowser(t *testing.T) {
	a, br, csrf := managedApp(t)
	for i := 0; i < 24; i++ {
		if err := a.MCP.Upstreams.Create(mcp.Upstream{Alias: fmt.Sprintf("u%02d", i), URL: "http://127.0.0.1:1/mcp", AuthKind: mcp.AuthNone, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if res, _ := br.post("/admin/upstreams/save", stdioForm(csrf, "proc", nil)); flashKind(res) != "ok" {
		t.Fatal("create the managed upstream")
	}
	for i := 0; i < 18; i++ {
		br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {fmt.Sprintf("key %02d", i)}})
	}
	now := time.Now()
	for i := 0; i < 18; i++ {
		seedClient(t, a, fmt.Sprintf("c%02d", i), "admin", now.Add(-time.Hour), now, "https://c.example/cb")
	}
	seedClient(t, a, "stale-one", "dcr", now.Add(-40*24*time.Hour), time.Time{}, "https://a.example/cb")
	seedClient(t, a, "stale-two", "dcr", now.Add(-50*24*time.Hour), time.Time{}, "https://b.example/cb")
	oa, _ := openAIUpstream(t, "gpt-x")
	runBrowserScript(t, "inplace.js", br.ts.URL, br, oa.URL+"/v1")
}
