package app

import (
	"strings"
	"testing"
)

// The two structured fields use the one shared code box: the textarea stays the form field (same name, plain markup, so
// the page works without script), and the colours come from app.js and app.css under the unchanged CSP.
func TestCodeBoxIsWiredIntoBothFields(t *testing.T) {
	_, _, br, _ := signedIn(t, nil)
	_, imp := br.get("/admin/upstreams/import")
	_, form := br.get("/admin/upstreams/new")
	for name, c := range map[string]struct{ page, want string }{
		"import":  {imp, `name="json" rows="12" spellcheck="false" autocomplete="off" data-code="json" data-json-check>`},
		"openapi": {form, `data-code="auto" data-oa-spec-text></textarea>`},
	} {
		if !strings.Contains(c.page, c.want) {
			t.Errorf("%s page: the textarea lacks the code box marker %q", name, c.want)
		}
		if strings.Contains(c.page, "<pre") || strings.Contains(c.page, "codebox") {
			t.Errorf("%s page: the code box is built by script, the served page stays a plain textarea", name)
		}
	}
	if !strings.Contains(form, "data-code-status") {
		t.Error("the OpenAPI box has a line that says what the text reads as")
	}
	r, _ := br.get("/admin/upstreams/new")
	csp := r.Header.Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'") || strings.Contains(csp, "unsafe") || strings.Contains(csp, "http") {
		t.Errorf("the CSP must not be loosened for the editor: %q", csp)
	}
	_, js := br.get("/admin/static/app.js")
	_, css := br.get("/admin/static/app.css")
	for _, bad := range []string{"createElement(\"style\")", "createElement('style')", ".style.cssText", "setAttribute(\"style\"", "eval(", "new Function("} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js must not use %s (CSP)", bad)
		}
	}
	for _, class := range []string{".codebox", ".t-k", ".t-s", ".t-n", ".t-w", ".t-p", ".t-c", ".t-t", ".t-a", ".t-x", ".t-e", ".t-m"} {
		if !strings.Contains(css, class) {
			t.Errorf("app.css lacks a rule for %s", class)
		}
	}
	if !strings.Contains(strings.SplitN(css, "\n", 4)[1], ".codebox") {
		t.Error("line 2 of app.css lists the components; add .codebox")
	}
}

// In a real browser (optional, same switches as the layout sweep): colours, bracket match, error marker, auto-indent,
// scroll in step, very long text, and no Content-Security-Policy violation.
func TestCodeBoxInBrowser(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	runBrowserScript(t, "codebox.js", ts.URL, br)
}
