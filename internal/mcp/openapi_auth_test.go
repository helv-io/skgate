package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/helv-io/skgate/internal/openapi"
)

// keyAPI accepts one key in one place and refuses everything else with 401.
type keyAPI struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newKeyAPI(t *testing.T, accept func(*http.Request) bool) *keyAPI {
	f := &keyAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if !accept(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1}]`))
	}))
	t.Cleanup(f.Close)
	return f
}

func addAuto(t *testing.T, e *env, alias, base, spec, key string) {
	t.Helper()
	if err := e.srv.Upstreams.Create(Upstream{Alias: alias, Kind: KindOpenAPI, URL: base, AuthKind: AuthAuto, AuthValue: key, Enabled: true, IncludeInMCP: true}); err != nil {
		t.Fatal(err)
	}
	sel := openapi.Selection{Enabled: map[string]bool{"GET /notes": true}}
	if err := e.srv.Upstreams.SetOpenAPI(alias, OpenAPIConfig{Spec: spec, Selection: sel}); err != nil {
		t.Fatal(err)
	}
}

func TestOneKeyFollowsTheDescription(t *testing.T) {
	for name, tc := range map[string]struct {
		scheme string
		check  func(*http.Request) bool
	}{
		"bearer": {`{"type":"http","scheme":"bearer"}`, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer KEY" }},
		"header": {`{"type":"apiKey","in":"header","name":"X-Custom"}`, func(r *http.Request) bool { return r.Header.Get("X-Custom") == "KEY" }},
		"query":  {`{"type":"apiKey","in":"query","name":"token"}`, func(r *http.Request) bool { return r.URL.Query().Get("token") == "KEY" }},
		"basic":  {`{"type":"http","scheme":"basic"}`, func(r *http.Request) bool { u, p, ok := r.BasicAuth(); return ok && u == "al" && p == "pw" }},
	} {
		e := newEnv(t, nil)
		api := newKeyAPI(t, tc.check)
		spec := `{"openapi":"3.0.0","info":{"title":"T","version":"1"},"security":[{"s":[]}],"components":{"securitySchemes":{"s":` + tc.scheme + `}},"paths":{"/notes":{"get":{"operationId":"listNotes"}}}}`
		key := "KEY"
		if name == "basic" {
			key = "al:pw"
		}
		addAuto(t, e, "n", api.URL, spec, key)
		tr := e.srv.Test(t.Context(), "n")
		if !tr.OK || tr.Error != "" {
			t.Errorf("%s: %+v", name, tr)
		}
		if name == "query" && (len(tr.Warnings) == 0 || !strings.Contains(strings.Join(tr.Warnings, " "), "web address")) {
			t.Errorf("a key in the query string gets a warning: %v", tr.Warnings)
		}
		if name != "query" && len(tr.Warnings) != 0 {
			t.Errorf("%s: unexpected warnings %v", name, tr.Warnings)
		}
		if len(api.seen) != 1 {
			t.Errorf("%s: a stated scheme needs one request, saw %v", name, api.seen)
		}
	}
}

func TestOneKeyIsFoundByTryingWhenTheDescriptionIsSilent(t *testing.T) {
	e := newEnv(t, nil)
	api := newKeyAPI(t, func(r *http.Request) bool { return r.Header.Get("Api-Key") == "KEY" })
	addAuto(t, e, "n", api.URL, oaSpec, "KEY")
	tr := e.srv.Test(t.Context(), "n")
	if !tr.OK || tr.Way != "header:Api-Key" {
		t.Fatalf("%+v", tr)
	}
	for _, s := range api.seen {
		if s != "GET /notes" {
			t.Errorf("only a safe GET: %s", s)
		}
	}
	u, _ := e.srv.Upstreams.Get("n")
	if u.DetectedKind != "header:Api-Key" {
		t.Fatalf("the way that worked is stored: %q", u.DetectedKind)
	}
	// the next call goes straight the stored way
	api.mu.Lock()
	api.seen = nil
	api.mu.Unlock()
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp/n", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"listNotes","arguments":{}}}`)
	if res, _ := m["result"].(map[string]any); res == nil || res["isError"] != false || len(api.seen) != 1 {
		t.Fatalf("call: %v saw %v", m, api.seen)
	}
	// editing the key forgets what was found
	if err := e.srv.Upstreams.Update(Upstream{Alias: "n", Kind: KindOpenAPI, URL: api.URL, AuthKind: AuthAuto, AuthValue: "OTHER", Enabled: true}, true); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.srv.Upstreams.Get("n"); u.DetectedKind != "" {
		t.Errorf("detection kept after a new key: %q", u.DetectedKind)
	}
}

func TestOneKeyFirstUseFindsTheWayAndRetriesAReadOnce(t *testing.T) {
	e := newEnv(t, nil)
	api := newKeyAPI(t, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Token KEY" })
	addAuto(t, e, "n", api.URL, oaSpec, "KEY")
	key, _, _ := e.keys.Create("t")
	_, m := e.rpc(key, "/mcp/n", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"listNotes","arguments":{}}}`)
	res, _ := m["result"].(map[string]any)
	if res == nil || res["isError"] != false {
		t.Fatalf("first use should work after finding the way: %v", m)
	}
	if u, _ := e.srv.Upstreams.Get("n"); u.DetectedKind != "token" {
		t.Errorf("stored way: %q", u.DetectedKind)
	}
}

func TestOneKeyRefusedEveryWayAndNothingGuessedForQuery(t *testing.T) {
	e := newEnv(t, nil)
	api := newKeyAPI(t, func(r *http.Request) bool { return false })
	addAuto(t, e, "n", api.URL, oaSpec, "KEY")
	tr := e.srv.Test(t.Context(), "n")
	if tr.OK || tr.Error != "The server refused the key." {
		t.Fatalf("%+v", tr)
	}
	for _, s := range api.seen {
		if strings.Contains(s, "?") {
			t.Errorf("a key is never tried in the query string: %s", s)
		}
	}
	if u, _ := e.srv.Upstreams.Get("n"); u.DetectedKind != "" {
		t.Errorf("nothing worked, nothing stored: %q", u.DetectedKind)
	}
	// the server cannot be reached
	api.Close()
	if tr := e.srv.Test(t.Context(), "n"); tr.OK || !strings.Contains(tr.Error, "cannot reach") {
		t.Errorf("unreachable: %+v", tr)
	}
}

func TestOneKeyValidation(t *testing.T) {
	u := Upstream{Alias: "a", Kind: KindOpenAPI, URL: "https://api.example.com", AuthKind: AuthAuto, AuthValue: "k", AuthName: "ignored", Enabled: true}
	if err := u.Validate(); err != nil || u.AuthName != "" {
		t.Errorf("auto with a key: %v name=%q", err, u.AuthName)
	}
	u.AuthValue = ""
	if u.Validate() == nil {
		t.Error("auto without a key accepted")
	}
}
