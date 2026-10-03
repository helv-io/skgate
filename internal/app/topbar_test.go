package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The top bar is one shared markup on every page: the current page name for phones, a Menu button that controls the
// panel holding the links, and the current link marked. The stylesheet makes it sticky below the toasts.
func TestTopBarMarkupAndStickyRules(t *testing.T) {
	_, br := menuApp(t)
	names := map[string]string{"/admin": "status", "/admin/keys": "keys", "/admin/upstreams": "mcp upstreams", "/admin/clients": "oauth clients", "/admin/upstreams/alpha/test": "mcp upstreams", "/admin/upstreams/new": "mcp upstreams"}
	for path, want := range names {
		_, body := br.get(path)
		if n := strings.Count(body, "<header"); n != 1 {
			t.Fatalf("%s: %d headers", path, n)
		}
		if !strings.Contains(body, `<span class="here" data-nav-here>`+want+`</span>`) {
			t.Errorf("%s: the page name should be %q:\n%s", path, want, body[strings.Index(body, "<header"):strings.Index(body, "</nav>")])
		}
		if !strings.Contains(body, `<button type="button" class="act nav-btn" aria-expanded="false" aria-controls="site-nav" data-nav-button>Menu</button>`) || !strings.Contains(body, `<nav class="nav-panel" id="site-nav" aria-label="Main">`) {
			t.Errorf("%s: the Menu button must control the nav panel", path)
		}
		if got := len(regexp.MustCompile(`aria-current="page"`).FindAllString(body, -1)); got != 1 {
			t.Errorf("%s: %d links marked current, want 1", path, got)
		}
		for _, part := range []string{`class="ver`, `action="/admin/logout"`} {
			head := body[strings.Index(body, "<nav"):strings.Index(body, "</nav>")]
			if !strings.Contains(head, part) {
				t.Errorf("%s: the panel lacks %s", path, part)
			}
		}
	}
	css, err := os.ReadFile(filepath.Join("..", "admin", "static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	c := string(css)
	for _, want := range []string{"header{position:sticky;top:0;z-index:var(--z-header);", "--z-header:30;", "scroll-padding-top:var(--header-h", "header[data-collapsed] .nav-panel{display:none}"} {
		if !strings.Contains(c, want) {
			t.Errorf("app.css lacks %q", want)
		}
	}
	// the only z-index above the header's belongs to the menus and the toasts, all through tokens
	for _, m := range regexp.MustCompile(`z-index:([^;}]+)`).FindAllStringSubmatch(c, -1) {
		if !strings.HasPrefix(m[1], "var(--z-") {
			t.Errorf("z-index %q must use a --z-* token", m[1])
		}
	}
}
