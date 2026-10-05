package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The tools page of an OpenAPI upstream tests a tool: a Test button, a dialog made from the input schema, and Run
// that calls the upstream through the same code a client's tools/call uses. Admin only.
func TestToolTesterRunsAnEnabledToolAsTheAdmin(t *testing.T) {
	_, _, br, csrf := signedIn(t, nil)
	_, calls, spec := countedAPI(t)
	if r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"tt"}, "enabled": {"1"}, "oa_spec_text": {spec}, "oa_auth_value": {"GOOD"}}); r.StatusCode != 303 {
		t.Fatalf("add: %d", r.StatusCode)
	}
	_, page := br.get("/admin/upstreams/tt/tools")
	for _, want := range []string{`data-dialog-open="#try-ping">Test</button>`, `id="try-ping"`, "data-try-run", "Run calls the upstream for real.", "Edit as JSON", "Edit inputs", ">Close</button>", "/admin/upstreams/tt/tools/try/run"} {
		if !strings.Contains(page, want) {
			t.Errorf("tools page lacks %q", want)
		}
	}
	if strings.Contains(page, "style=") && strings.Contains(page, "data-try") {
		// the page may carry style attributes elsewhere; the tester adds none
		_, tester, _ := strings.Cut(page, "data-try-edit")
		if strings.Contains(tester, "style=") {
			t.Error("the tester uses an inline style")
		}
	}
	info := func(name string) map[string]any {
		_, out := postInPlace(t, br, "/admin/upstreams/tt/tools/try/info", url.Values{"csrf": {csrf}, "name": {name}})
		return out
	}
	if out := info("ping"); out["name"] != "ping" || out["schema"] == nil || out["error"] != nil {
		t.Fatalf("info: %v", out)
	}
	if out := info("nope"); out["error"] == nil {
		t.Fatalf("an unknown tool has no info: %v", out)
	}
	run := func(name, args string) map[string]any {
		_, out := postInPlace(t, br, "/admin/upstreams/tt/tools/try/run", url.Values{"csrf": {csrf}, "name": {name}, "args": {args}})
		return out
	}
	n := calls.Load()
	out := run("ping", "{}")
	if out["ok"] != true || out["isError"] != false || !strings.Contains(out["text"].(string), "[]") || calls.Load() != n+1 {
		t.Fatalf("run: %v calls %d -> %d", out, n, calls.Load())
	}
	// nothing is called for arguments that are not an object, or for a tool that is off
	n = calls.Load()
	for _, c := range []struct{ name, args string }{{"ping", "[1]"}, {"ping", "{oops"}, {"nope", "{}"}} {
		if o := run(c.name, c.args); o["ok"] != false || o["error"] == nil {
			t.Errorf("%s %s: %v", c.name, c.args, o)
		}
	}
	if calls.Load() != n {
		t.Errorf("a refused run called the upstream: %d -> %d", n, calls.Load())
	}
	// signed out: no answer from the tester
	resp, err := http.PostForm(br.ts.URL+"/admin/upstreams/tt/tools/try/run", url.Values{"csrf": {csrf}, "name": {"ping"}, "args": {"{}"}})
	if err == nil {
		defer resp.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		k, _ := resp.Body.Read(buf)
		b.Write(buf[:k])
		if strings.Contains(b.String(), `"ok":true`) || calls.Load() != n {
			t.Errorf("signed out ran a tool: %d %s", resp.StatusCode, b.String())
		}
	}
	// without the token the run is refused as well
	if r, body := br.post("/admin/upstreams/tt/tools/try/run", url.Values{"name": {"ping"}, "args": {"{}"}}); r.StatusCode == 200 && strings.Contains(body, `"ok":true`) {
		t.Error("a run without the CSRF token went through")
	}
}

func TestToolTesterInBrowser(t *testing.T) {
	_, _, br, csrf := signedIn(t, nil)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(r.URL.Path, "/items/"), "limit": q.Get("limit"), "verbose": q.Get("verbose"), "kind": q.Get("kind")})
	}))
	t.Cleanup(api.Close)
	spec := fmt.Sprintf(`{"openapi":"3.0.0","info":{"title":"Items","version":"1"},"servers":[{"url":"%s"}],"paths":{"/items/{id}":{"get":{"operationId":"getItem","parameters":[`+
		`{"name":"id","in":"path","required":true,"description":"The item id. More words that do not belong in the helper line.","schema":{"type":"string"}},`+
		`{"name":"limit","in":"query","schema":{"type":"integer","default":10}},`+
		`{"name":"verbose","in":"query","schema":{"type":"boolean"}},`+
		`{"name":"kind","in":"query","schema":{"type":"string","enum":["a","b"]}},`+
		`{"name":"tags","in":"query","schema":{"type":"array","items":{"type":"string"}}}]}}}}`, api.URL)
	if r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"tt"}, "enabled": {"1"}, "oa_spec_text": {spec}, "oa_skip_check": {"1"}}); r.StatusCode != 303 {
		t.Fatalf("add: %d", r.StatusCode)
	}
	runBrowserScript(t, "trytool.js", br.ts.URL, br)
}
