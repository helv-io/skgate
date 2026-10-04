package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// A description only looks clickable when a click shows something: a row with nothing to expand is plain text
// (no <details>, no <summary>), and the Name/Description table is split about 30/70.
func TestToolDescriptionsAreClickableOnlyWhenTheyOpen(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(b), `"initialize"`):
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"fake","version":"0.1"}}}`)
		case strings.Contains(string(b), `"tools/list"`):
			mid := "Fits on a wide screen but not on a phone, with a few more words"
			long := strings.Repeat("A long single line that never fits in the column, however wide the window is. ", 4)
			io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[`+
				`{"name":"short_tool","description":"Does one thing."},`+
				`{"name":"more_tool","description":"Has a first line.\nAnd a second line that is only shown when opened."},`+
				`{"name":"mid_tool","description":"`+mid+`"},`+
				`{"name":"long_tool","description":"`+long+`"},`+
				`{"name":"a_tool_with_a_rather_long_name_that_has_to_wrap_somewhere_in_the_name_column","description":"Named at length."}]}}`)
		default:
			w.WriteHeader(202)
		}
	}))
	t.Cleanup(up.Close)
	_, ts, br, csrf := signedIn(t, nil)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "alias": {"fake"}, "url": {up.URL}, "auth_kind": {"auto"}, "enabled": {"1"}})
	_, page := br.get("/admin/upstreams/fake/test")
	if n := strings.Count(page, "<details class=\"desc\">"); n != 1 {
		t.Errorf("only the row with more text is a details block, got %d", n)
	}
	if !strings.Contains(page, `<summary>Has a first line.</summary><p>And a second line that is only shown when opened.</p>`) {
		t.Error("the row with more text opens to it")
	}
	for _, plain := range []string{"Does one thing.", "Named at length."} {
		if !regexp.MustCompile(`<span class="desc-line" data-desc-line>` + regexp.QuoteMeta(plain) + `</span>`).MatchString(page) {
			t.Errorf("%q has nothing to expand: plain text without a title or summary", plain)
		}
	}
	if !strings.Contains(page, `<table class="table split" id="tools-table">`) {
		t.Error("the tools table uses the shared split (about 30/70) layout")
	}
	runBrowserScript(t, "descline.js", ts.URL, br)
}
