package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/helv-io/skgate/internal/mcp"
)

// countedAPI takes the bearer key GOOD on /ping and counts the requests it gets.
func countedAPI(t *testing.T) (*httptest.Server, *atomic.Int32, string) {
	n := new(atomic.Int32)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Header.Get("Authorization") != "Bearer GOOD" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	t.Cleanup(ts.Close)
	spec := fmt.Sprintf(`{"openapi":"3.0.0","info":{"title":"Counted","version":"1"},"servers":[{"url":"%s"}],"security":[{"b":[]}],`+
		`"components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}},"paths":{"/ping":{"get":{"operationId":"ping"}}}}`, ts.URL)
	return ts, n, spec
}

// postInPlace posts the form the way the page script does and returns the status and the answer.
func postInPlace(t *testing.T, br *browser, path string, v url.Values) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", br.ts.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Follow", "1")
	resp, err := br.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func toastOf(out map[string]any) (string, string) {
	m, _ := out["toast"].(map[string]any)
	k, _ := m["k"].(string)
	s, _ := m["m"].(string)
	return k, s
}

func TestOpenAPIAddChecksTheConnectionAndSkipCheckSavesAnyway(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	_, calls, spec := countedAPI(t)
	form := func(alias, key string, extra url.Values) url.Values {
		v := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {alias}, "enabled": {"1"}, "oa_spec_text": {spec}, "oa_auth_value": {key}}
		for k, x := range extra {
			v[k] = x
		}
		return v
	}
	// no key: the public spec was fine, the first protected read is not
	st, out := postInPlace(t, br, "/admin/upstreams/save", form("m1", "", nil))
	if k, m := toastOf(out); st != 200 || k != "bad" || m != "the server needs a key" || out["to"] != nil {
		t.Fatalf("no key: %d %v", st, out)
	}
	if _, ok := a.MCP.Upstreams.Get("m1"); ok {
		t.Fatal("stored although the check failed")
	}
	if st, out = postInPlace(t, br, "/admin/upstreams/save", form("m1", "WRONG", nil)); func() bool { _, m := toastOf(out); return m != "the server refused the key" }() {
		t.Fatalf("wrong key: %d %v", st, out)
	}
	// Skip check saves without asking
	before := calls.Load()
	st, out = postInPlace(t, br, "/admin/upstreams/save", form("m1", "", url.Values{"oa_skip_check": {"1"}}))
	if k, _ := toastOf(out); st != 200 || k != "ok" || out["to"] != "/admin/upstreams/m1/tools" || calls.Load() != before {
		t.Fatalf("skip: %d %v calls %d to %d", st, out, before, calls.Load())
	}
	if _, ok := a.MCP.Upstreams.Get("m1"); !ok {
		t.Fatal("not stored with Skip check")
	}
	// the right key passes
	if st, out = postInPlace(t, br, "/admin/upstreams/save", form("m2", "GOOD", nil)); func() bool { k, _ := toastOf(out); return k != "ok" }() {
		t.Fatalf("good key: %d %v", st, out)
	}
}

func TestOpenAPIEditChecksOnlyWhenWhatReachesTheServerChanged(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	api, calls, spec := countedAPI(t)
	base := url.Values{"csrf": {csrf}, "mode": {"edit"}, "kind": {"openapi"}, "alias": {"e"}, "enabled": {"1"}}
	add := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"e"}, "enabled": {"1"}, "oa_spec_text": {spec}, "oa_auth_value": {"GOOD"}}
	if r, _ := br.post("/admin/upstreams/save", add); r.StatusCode != 303 {
		t.Fatalf("add: %d", r.StatusCode)
	}
	n := calls.Load()
	// nothing that reaches the server changed: no call
	v := url.Values{}
	for k, x := range base {
		v[k] = x
	}
	v.Set("oa_url", api.URL)
	if r, _ := br.post("/admin/upstreams/save", v); r.StatusCode != 303 || flashKind(r) != "ok" || calls.Load() != n {
		t.Fatalf("unchanged edit: %d %q calls %d -> %d", r.StatusCode, flashKind(r), n, calls.Load())
	}
	// a new key is checked, and a refused one keeps the stored key
	v.Set("oa_auth_value", "BAD")
	if st, out := postInPlace(t, br, "/admin/upstreams/e/save", v); func() bool { k, m := toastOf(out); return st != 200 || k != "bad" || m != "the server refused the key" }() {
		t.Fatalf("new key: %d %v", st, out)
	}
	if u, _ := a.MCP.Upstreams.Get("e"); u.AuthValue != "GOOD" {
		t.Errorf("a refused key replaced the stored one: %q", u.AuthValue)
	}
	// a new base URL is checked
	v.Del("oa_auth_value")
	v.Set("oa_url", api.URL+"/other")
	if st, out := postInPlace(t, br, "/admin/upstreams/e/save", v); func() bool { k, _ := toastOf(out); return st != 200 || k != "ok" }() {
		t.Fatalf("new base URL with the stored key: %d %v", st, out)
	}
	if calls.Load() == n {
		t.Error("a changed base URL must be checked")
	}
}

func TestOpenAPIFormSaysWhatTheCheckDoes(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	_, page := br.get("/admin/upstreams/new")
	for _, want := range []string{"The first GET without parameters checks the connection.", `name="oa_skip_check"`, "Skip check"} {
		if !strings.Contains(page, want) {
			t.Errorf("add form lacks %q", want)
		}
	}
	_, _, spec := countedAPI(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"has"}, "enabled": {"1"}, "oa_spec_text": {spec}, "oa_auth_value": {"GOOD"}})
	_, edit := br.get("/admin/upstreams/has/edit")
	if !strings.Contains(edit, "The first GET without parameters checks the connection.") || !strings.Contains(edit, `name="oa_skip_check"`) {
		t.Error("the edit form has the same line and Skip check")
	}
	// a description with nothing to ask shows no line at all
	a.Admin.NoSaveTest = true
	none := `{"openapi":"3.0.0","info":{"title":"N","version":"1"},"servers":[{"url":"https://api.example.com"}],"paths":{"/x/{id}":{"get":{"operationId":"x","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]}}}}`
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"none"}, "enabled": {"1"}, "oa_spec_text": {none}})
	_, edit = br.get("/admin/upstreams/none/edit")
	if strings.Contains(edit, "oa_skip_check") || strings.Contains(edit, "checks the connection") {
		t.Error("nothing to ask, nothing to say")
	}
	// Check description tells the page whether the line applies
	_, body := br.post("/admin/openapi/check", url.Values{"csrf": {csrf}, "oa_spec_text": {none}})
	var m map[string]any
	json.Unmarshal([]byte(body), &m)
	if m["probe"] != false {
		t.Errorf("probe for a description with none: %v", m["probe"])
	}
	_, body = br.post("/admin/openapi/check", url.Values{"csrf": {csrf}, "oa_spec_text": {spec}})
	m = nil
	json.Unmarshal([]byte(body), &m)
	if m["probe"] != true {
		t.Errorf("probe for a description with one: %v", m["probe"])
	}
	_ = mcp.AuthAuto
}
