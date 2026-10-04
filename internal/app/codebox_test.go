package app

import (
	"strings"
	"testing"
)

// The two structured fields use the one shared editor (CodeMirror 6, vendored as static/codemirror.js): the textarea
// stays the form field (same name, plain markup, so the page works without script), the bundle is served from our own
// origin, and the CSP is unchanged.
func TestCodeBoxIsWiredIntoBothFields(t *testing.T) {
	_, _, br, _ := signedIn(t, nil)
	_, imp := br.get("/admin/upstreams/import")
	_, form := br.get("/admin/upstreams/new")
	for name, c := range map[string]struct{ page, want string }{
		"import":  {imp, `name="json" rows="12" spellcheck="false" autocomplete="off" data-code="json" data-json-check>`},
		"openapi": {form, `data-code="auto" data-oa-spec-text></textarea>`},
	} {
		if !strings.Contains(c.page, c.want) {
			t.Errorf("%s page: the textarea lacks the editor marker %q", name, c.want)
		}
		if !strings.Contains(c.page, `<script src="/admin/static/codemirror.js" defer></script>`) {
			t.Errorf("%s page: loads the editor from our own origin", name)
		}
		if strings.Contains(c.page, "codehost") {
			t.Errorf("%s page: the editor is built by script, the served page stays a plain textarea", name)
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
	resp, bundle := br.get("/admin/static/codemirror.js")
	if resp.StatusCode != 200 || len(bundle) < 100000 || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("codemirror.js: %d, %d bytes, %q", resp.StatusCode, len(bundle), resp.Header.Get("Content-Type"))
	}
	for _, bad := range []string{"fetch(", "XMLHttpRequest", "importScripts", "eval(", "new Function(", "https://cdn", "unpkg.com", "jsdelivr"} {
		if strings.Contains(bundle, bad) {
			t.Errorf("codemirror.js must not contain %q (no runtime fetch, no CDN, no eval)", bad)
		}
	}
	_, js := br.get("/admin/static/app.js")
	_, css := br.get("/admin/static/app.css")
	if strings.Contains(js, "Code box") || strings.Contains(css, ".codebox") {
		t.Error("the hand-written code box is gone; only the vendored editor remains")
	}
	for _, class := range []string{".codehost", "textarea.code-src", "--code-size"} {
		if !strings.Contains(css, class) {
			t.Errorf("app.css lacks %s", class)
		}
	}
	if !strings.Contains(strings.SplitN(css, "\n", 4)[1], ".codehost") {
		t.Error("line 2 of app.css lists the components; add .codehost")
	}
}

// In a real browser (optional, same switches as the layout sweep): colours, error underline, bracket match, auto-indent,
// the textarea kept as the field, 16px on touch screens, and no Content-Security-Policy violation.
func TestCodeBoxInBrowser(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	runBrowserScript(t, "codebox.js", ts.URL, br)
}
