package app

import (
	"net/url"
	"strings"
	"testing"
)

// The status page opens with the two client URLs and a copy button each, and its pills read as phrases:
// "grok-4.7 · reasoning low" for the helper model and "1 alias" for the aliases.
func TestStatusOverviewAndReadablePills(t *testing.T) {
	up, _ := modelsUpstream(t, "grok-4.7", "grok-mini")
	_, ts, br, csrf, _ := signedInProvider(t, up)
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	_, page := br.get("/admin")
	plain := strings.ReplaceAll(page, "<wbr>", "")
	for _, w := range []string{"<h2>Overview</h2>", `<th>OpenAI API</th><td><button type="button" class="copybox" data-copy-text="` + ts.URL + `/v1"`, `<th>MCP</th><td><button type="button" class="copybox" data-copy-text="` + ts.URL + `/mcp"`} {
		if !strings.Contains(plain, w) {
			t.Errorf("status page misses %q", w)
		}
	}
	if strings.Index(page, "<h2>Overview</h2>") > strings.Index(page, "<h2>Sign-in</h2>") {
		t.Error("the overview comes first")
	}
	if !strings.Contains(page, ">no model</span>") || !strings.Contains(page, ">no aliases</span>") {
		t.Errorf("empty states are phrases too:\n%s", page)
	}
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-4.7"}, "effort": {"low"}, "timeout": {"120"}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"grok-mini"}})
	_, page = br.get("/admin")
	for _, w := range []string{">grok-4.7 \u00b7 reasoning low</span>", ">1 alias</span>"} {
		if !strings.Contains(page, w) {
			t.Errorf("status page misses the pill %q:\n%s", w, page)
		}
	}
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-4.7"}, "effort": {"auto"}, "timeout": {"120"}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"quick"}, "target": {"grok-mini"}})
	_, page = br.get("/admin")
	for _, w := range []string{">grok-4.7 \u00b7 reasoning auto</span>", ">2 aliases</span>"} {
		if !strings.Contains(page, w) {
			t.Errorf("status page misses the pill %q", w)
		}
	}
}
