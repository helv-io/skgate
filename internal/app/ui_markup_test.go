package app

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/oidctest"
)

// On phones every list table becomes a stack of cards, and each cell is titled by its data-label, so
// every cell of every list table carries one that matches its column header.
func TestTableCellsCarryTheirLabels(t *testing.T) {
	up, _ := modelsUpstream(t, "grok-4", "grok-mini")
	_, _, br, csrf, _ := signedInProvider(t, up)
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/upstreams/save", stdioForm(csrf, "m", nil))
	br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"k"}})
	br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"c"}, "redirects": {"https://x.example/cb"}, "method": {"client_secret_post"}})
	table := regexp.MustCompile(`(?s)<table class="table([^"]*)">(.*?)</table>`)
	head := regexp.MustCompile(`<th[^>]*>([^<]*)</th>`)
	row := regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	cell := regexp.MustCompile(`<td([^>]*)>`)
	label := regexp.MustCompile(`data-label="([^"]*)"`)
	seen := 0
	for _, path := range []string{"/admin", "/admin/keys", "/admin/clients", "/admin/upstreams", "/admin/upstreams/logs?alias=m", "/admin/providers/grok"} {
		_, page := br.get(path)
		for _, tb := range table.FindAllStringSubmatch(page, -1) {
			if strings.Contains(tb[1], "kv") {
				continue
			}
			var cols []string
			for _, h := range head.FindAllStringSubmatch(tb[2], -1) {
				cols = append(cols, strings.TrimSpace(h[1]))
			}
			for _, r := range row.FindAllStringSubmatch(tb[2], -1) {
				cells := cell.FindAllStringSubmatch(r[1], -1)
				if len(cells) == 0 || strings.Contains(r[1], `class="empty"`) {
					continue
				}
				for i, c := range cells {
					if strings.Contains(c[1], "actions-cell") {
						continue // the button row has no title on a card
					}
					m := label.FindStringSubmatch(c[1])
					if m == nil {
						t.Errorf("%s: cell %d of a list table has no data-label", path, i)
						continue
					}
					seen++
					if i < len(cols) && cols[i] != "" && m[1] != cols[i] {
						t.Errorf("%s: cell %d labelled %q under column %q", path, i, m[1], cols[i])
					}
				}
			}
		}
	}
	if seen < 10 {
		t.Fatalf("only %d labelled cells found, the check is not looking at the tables", seen)
	}
}

// Copy URL puts the client URL of an upstream on the clipboard; it is offered only for enabled upstreams (the
// ones /mcp/<alias> serves) and never carries a key.
func TestCopyURLOnlyForEnabledUpstreams(t *testing.T) {
	_, br, csrf := managedApp(t)
	br.post("/admin/upstreams/save", stdioForm(csrf, "on", nil))
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"off"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"auto"}})
	_, page := br.get("/admin/upstreams")
	copyBtn := regexp.MustCompile(`data-copy-text="([^"]*)"`)
	got := copyBtn.FindAllStringSubmatch(page, -1)
	if len(got) != 1 || !strings.HasSuffix(got[0][1], "/mcp/on") {
		t.Fatalf("want one copy button, for the enabled upstream: %v", got)
	}
	if strings.ContainsAny(got[0][1], "?&=") || strings.Contains(got[0][1], "sk-") || strings.Contains(got[0][1], "super-secret") {
		t.Fatalf("copied URL must not carry a key or secret: %q", got[0][1])
	}
	if !strings.Contains(page, `data-copied="URL copied"`) {
		t.Error("copy result is a toast message")
	}
}

// The consent screen and the other standalone screens are built from the shared layout and stylesheet only,
// in the one centered card, and the consent form posts both decisions to /authorize.
func TestSoloScreensUseSharedStyles(t *testing.T) {
	_, ts, br, _ := signedIn(t, func(c *config.Config, _ *oidctest.Provider) { c.RequireConsent = true })
	clientID := registerClient(t, ts, clientRedirect)
	_, challenge := pkcePair()
	_, consent := br.get(authorizePath(clientID, clientRedirect, challenge))
	_, signedOut := br.get("/admin/signed-out")
	_, authErr := br.get("/admin/oidc/callback?error=access_denied&state=x")
	for name, page := range map[string]string{"consent": consent, "signed out": signedOut, "sign-in error": authErr} {
		for _, want := range []string{`href="/admin/static/app.css"`, `<main class="solo"`, `<div class="login"><div class="card">`, `class="brandmark"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: missing %s", name, want)
			}
		}
		for _, bad := range []string{" style=", "<style", "<header", "<dialog", "onclick="} {
			if strings.Contains(page, bad) {
				t.Errorf("%s: standalone screens use shared styles only and no navigation, found %q", name, bad)
			}
		}
	}
	for _, want := range []string{`action="/authorize"`, `name="decision" value="deny"`, `name="decision" value="approve"`, `class="choices"`, `class="solo-name"`, "Authorize MCP access"} {
		if !strings.Contains(consent, want) {
			t.Errorf("consent: missing %s", want)
		}
	}
}

// The solo layout is centered both ways in the shared stylesheet, without scrolling sideways.
func TestSoloLayoutRulesInSharedStylesheet(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "admin", "static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{"main.solo{max-width:none;min-height:100vh;min-height:100dvh;display:flex;align-items:center;justify-content:center", ".login{width:min(460px,100%)", ".login .card{margin:0"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %q", want)
		}
	}
	for _, want := range []string{"overflow-wrap:anywhere", "attr(data-label)"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %q", want)
		}
	}
}
