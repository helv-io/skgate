package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// slashMCP is a strict upstream: only one spelling of its path exists, the other one answers with
// the given status (404 by default, 405 or a redirect like real frameworks do).
type slashMCP struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newSlashMCP(t *testing.T, good string, otherStatus int, redirectOther bool) *slashMCP {
	f := &slashMCP{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if r.URL.Path != good {
			if redirectOther {
				w.Header().Set("Location", "http://internal-host.invalid"+good)
				w.WriteHeader(otherStatus)
				return
			}
			w.WriteHeader(otherStatus)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(200)
			return
		}
		var m struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		_ = jsonUnmarshal(b, &m)
		w.Header().Set("Content-Type", "application/json")
		switch m.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"strict","version":"1"}}}`)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			io.WriteString(w, `{"jsonrpc":"2.0","id":`+jsonNum(m.ID)+`,"result":{"tools":[{"name":"ping_tool"}]}}`)
		default:
			io.WriteString(w, `{"jsonrpc":"2.0","id":`+jsonNum(m.ID)+`,"result":{"ok":true}}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *slashMCP) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.paths {
		if strings.HasSuffix(p, " "+path) {
			n++
		}
	}
	return n
}

func TestOutboundSlashToggleBothDirections(t *testing.T) {
	cases := []struct {
		name, configured, good string
		otherStatus            int
		redirect               bool
		fixed                  bool // the stored URL is corrected to the spelling that works
	}{
		{"configured with slash, upstream wants none (404)", "/mcp/", "/mcp", 404, false, true},
		{"configured without slash, upstream wants slash (404)", "/mcp", "/mcp/", 404, false, true},
		{"405 suggests the mismatch", "/mcp", "/mcp/", 405, false, true},
		{"redirect to an internal host is not followed, toggling works", "/mcp/", "/mcp", 307, true, true},
		{"the configured spelling works: nothing to correct", "/mcp", "/mcp", 404, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			f := newSlashMCP(t, c.good, c.otherStatus, c.redirect)
			e.srv.Upstreams.Create(Upstream{Alias: "u", URL: f.URL + c.configured, AuthKind: AuthNone, Enabled: true, IncludeInMCP: true})
			key, _, _ := e.keys.Create("t")
			h := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
			// transparent proxy
			r := e.do("POST", "/mcp/u", h, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if r.StatusCode != 200 || !strings.Contains(string(b), `"strict"`) || r.Header.Get("Location") != "" {
				t.Fatalf("proxy: %d %s", r.StatusCode, b)
			}
			up, _ := e.srv.Upstreams.Get("u")
			if want := f.URL + c.good; up.URL != want {
				t.Fatalf("stored URL %q, want the working spelling %q", up.URL, want)
			}
			// the second call goes straight to the good spelling: no more requests to the bad one
			bad := c.configured
			if c.configured == c.good {
				bad = ""
			}
			before := f.count(bad)
			r = e.do("POST", "/mcp/u", h, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
			r.Body.Close()
			if r.StatusCode != 200 || (bad != "" && f.count(bad) != before) {
				t.Fatalf("second call: %d, requests to the wrong spelling went %d -> %d", r.StatusCode, before, f.count(bad))
			}
			// aggregator uses the learned spelling too
			_, m := e.rpc(key, "/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
			if got := strings.Join(toolNames(t, m), ","); got != "u-ping_tool" {
				t.Fatalf("aggregate: %q %v", got, m)
			}
			// the Test button works and shows nothing about slashes
			res := e.srv.Test(t.Context(), "u")
			if !res.OK {
				t.Fatalf("test result: %+v", res)
			}
		})
	}
}

func TestAutoDetectLearnsSlashVariant(t *testing.T) {
	e := newEnv(t, nil)
	f := newSlashMCP(t, "/mcp", 404, false)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: f.URL + "/mcp/", AuthKind: AuthAuto, Enabled: true})
	up, err := e.srv.Redetect(t.Context(), "u")
	if err != nil {
		t.Fatal(err)
	}
	if up.DetectedKind != "none" || up.URL != f.URL+"/mcp" || !strings.Contains(up.DetectedNote, "valid initialize result") {
		t.Fatalf("detect: kind=%q url=%q note=%s", up.DetectedKind, up.URL, up.DetectedNote)
	}
	if got, _ := e.srv.Upstreams.Get("u"); got.URL != f.URL+"/mcp" {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestGenuine404IsReportedUnchanged(t *testing.T) {
	e := newEnv(t, nil)
	f := newSlashMCP(t, "/elsewhere", 404, false)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: f.URL + "/mcp", AuthKind: AuthNone, Enabled: true})
	key, _, _ := e.keys.Create("t")
	r := e.do("POST", "/mcp/u", map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}, `{}`)
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatalf("a real 404 must reach the client as 404, got %d", r.StatusCode)
	}
	if f.count("/mcp") != 1 || f.count("/mcp/") != 1 {
		t.Fatalf("expected exactly one retry: %v", f.paths)
	}
	if up, _ := e.srv.Upstreams.Get("u"); up.URL != f.URL+"/mcp" {
		t.Fatalf("nothing should change after a failure: %q", up.URL)
	}
}

func TestToggleSlash(t *testing.T) {
	for in, want := range map[string]string{
		"http://h:8080/mcp":  "http://h:8080/mcp/",
		"http://h:8080/mcp/": "http://h:8080/mcp",
		"http://h/a/b?x=1":   "http://h/a/b/?x=1",
		"http://h/a/b/?x=1":  "http://h/a/b?x=1",
		"https://h":          "https://h/",
		"http://h/mcp//":     "http://h/mcp",
	} {
		if got := ToggleSlash(in); got != want {
			t.Errorf("ToggleSlash(%q) = %q, want %q", in, got, want)
		}
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func jsonNum(id any) string {
	b, _ := json.Marshal(id)
	if len(b) == 0 || string(b) == "null" {
		return "0"
	}
	return string(b)
}

// The URL is only corrected while it is still what was probed: a concurrent edit wins.
func TestSetWorkingURLDoesNotOverwriteAnEdit(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Upstreams.Create(Upstream{Alias: "u", URL: "http://h/mcp/", AuthKind: AuthNone, Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "m", Kind: KindStdio, Command: "x", Enabled: true})
	if ok, err := e.srv.Upstreams.SetWorkingURL("u", "http://h/old", "http://h/old/"); err != nil || ok {
		t.Fatalf("stale URL must not apply: %v %v", ok, err)
	}
	if ok, _ := e.srv.Upstreams.SetWorkingURL("m", "", "http://h/"); ok {
		t.Fatal("managed upstreams have no URL to correct")
	}
	if ok, err := e.srv.Upstreams.SetWorkingURL("u", "http://h/mcp/", "http://h/mcp"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if got, _ := e.srv.Upstreams.Get("u"); got.URL != "http://h/mcp" {
		t.Fatal(got.URL)
	}
}

// url_variant rows written by earlier releases are folded into the URL once; running it again does nothing.
func TestNormalizeURLsFoldsLegacyVariant(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Upstreams.Create(Upstream{Alias: "t", URL: "http://h/mcp/", AuthKind: AuthNone, Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "a", URL: "http://h/x", AuthKind: AuthNone, Enabled: true})
	e.srv.Upstreams.Create(Upstream{Alias: "n", URL: "http://h/y/", AuthKind: AuthNone, Enabled: true})
	for alias, v := range map[string]string{"t": "toggled", "a": "as-is"} {
		if _, err := e.db.Exec(`UPDATE upstreams SET url_variant=? WHERE alias=?`, v, alias); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := e.srv.Upstreams.NormalizeURLs(); err != nil {
			t.Fatal(err)
		}
		for alias, want := range map[string]string{"t": "http://h/mcp", "a": "http://h/x", "n": "http://h/y/"} {
			if got, _ := e.srv.Upstreams.Get(alias); got.URL != want {
				t.Fatalf("pass %d %s: %q, want %q", i, alias, got.URL, want)
			}
		}
	}
	var left int
	e.db.QueryRow(`SELECT count(*) FROM upstreams WHERE url_variant<>''`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d rows keep a variant", left)
	}
}
