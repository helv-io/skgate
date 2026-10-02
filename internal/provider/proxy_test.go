package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/helv-io/skgate/internal/store"
	"github.com/helv-io/skgate/internal/vkeys"
)

type fakeBackend struct {
	base, fallback string
	token          string
	signedOut      bool
	headers        map[string]string
	refreshes      int32
}

func (f *fakeBackend) ID() string              { return "fake" }
func (f *fakeBackend) DefaultBase() string     { return f.base }
func (f *fakeBackend) DefaultFallback() string { return f.fallback }
func (f *fakeBackend) Headers(string) map[string]string {
	return f.headers
}
func (f *fakeBackend) Token(context.Context) (string, error) {
	if f.signedOut {
		return "", errors.New("not signed in")
	}
	return f.token, nil
}
func (f *fakeBackend) ForceRefresh(context.Context) (string, error) {
	atomic.AddInt32(&f.refreshes, 1)
	f.token = "REFRESHED"
	return f.token, nil
}

type rig struct {
	be    *fakeBackend
	set   Settings
	proxy *Proxy
	front *httptest.Server
	key   string
	db    *store.DB
	keys  *vkeys.Manager
	// last upstream request
	mu                      sync.Mutex
	path, query, auth, body string
}

func (r *rig) last() (path, query, auth, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.path, r.query, r.auth, r.body
}

const upModels = `{"object":"list","data":[{"id":"real-a","object":"model","owned_by":"x"},{"id":"real-b","object":"model","owned_by":"x"}]}`

func newRig(t *testing.T, handler func(w http.ResponseWriter, req *http.Request, body string)) *rig {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{db: db, set: Settings{KV: db}}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.path, r.query, r.auth, r.body = req.URL.Path, req.URL.RawQuery, req.Header.Get("Authorization"), string(b)
		r.mu.Unlock()
		if handler != nil {
			handler(w, req, string(b))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/v1/models" {
			io.WriteString(w, upModels)
			return
		}
		io.WriteString(w, `{"model":"echo","ok":true}`)
	}))
	t.Cleanup(up.Close)
	keys := vkeys.New(db)
	r.keys = keys
	r.key, _, _ = keys.Create("app")
	r.be = &fakeBackend{base: up.URL + "/v1", token: "LIVE-TOKEN"}
	r.proxy = NewProxy(r.be, r.set, keys)
	mux := http.NewServeMux()
	for _, p := range Patterns() {
		mux.Handle(p, r.proxy)
	}
	r.front = httptest.NewServer(mux)
	t.Cleanup(r.front.Close)
	return r
}

func (r *rig) do(t *testing.T, method, path, body string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, r.front.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+r.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func ids(t *testing.T, body string) []string {
	t.Helper()
	got := ModelIDs([]byte(body))
	if got == nil {
		t.Fatalf("not a model list: %s", body)
	}
	return got
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"/chat/completions": "/chat/completions", "/v1/chat/completions": "/chat/completions",
		"/api/chat/completions": "/chat/completions", "/api/v1/chat/completions": "/chat/completions",
		"/v1": "/", "/api": "/", "/api/v1": "/", "/v1/": "/", "/models": "/models", "/api/v1/models/x%2Fy": "/models/x%2Fy",
		"/apis/x": "/apis/x", "/v10/x": "/v10/x",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every spelling reaches the same upstream path with the provider's token, and the caller's key
// never leaves skgate.
func TestEverySpellingReachesTheSameEndpoint(t *testing.T) {
	r := newRig(t, nil)
	for _, pre := range []string{"", "/v1", "/api", "/api/v1"} {
		for _, ep := range []string{"/chat/completions", "/responses", "/embeddings", "/models"} {
			method := "POST"
			if ep == "/models" {
				method = "GET"
			}
			if st, _, _ := r.do(t, method, pre+ep+"?x=1", `{"model":"real-a"}`); st != 200 {
				t.Fatalf("%s%s: %d", pre, ep, st)
			}
			path, query, auth, _ := r.last()
			if path != "/v1"+ep || query != "x=1" || auth != "Bearer LIVE-TOKEN" {
				t.Fatalf("%s%s reached path=%q query=%q auth=%q", pre, ep, path, query, auth)
			}
		}
	}
}

// The proxy claims only the API paths: routes owned by others are not shadowed.
func TestPatternsDoNotShadowOtherRoutes(t *testing.T) {
	for _, p := range Patterns() {
		for _, other := range []string{"/mcp", "/admin", "/authorize", "/token", "/register", "/.well-known", "/sse", "/messages", "/healthz"} {
			if p == other || p == other+"/" || strings.HasPrefix(p, other+"/") {
				t.Errorf("pattern %q shadows %q", p, other)
			}
		}
	}
	mux := http.NewServeMux()
	for _, p := range Patterns() {
		mux.HandleFunc(p, func(http.ResponseWriter, *http.Request) {})
	}
	for _, path := range []string{"/mcp", "/mcp/x", "/admin", "/admin/keys", "/authorize", "/token", "/register", "/.well-known/oauth-authorization-server", "/sse", "/messages", "/healthz"} {
		req := httptest.NewRequest("GET", path, nil)
		if _, pat := mux.Handler(req); pat != "" {
			t.Errorf("%s matched proxy pattern %q", path, pat)
		}
	}
}

func TestProxyAuthAndNotSignedIn(t *testing.T) {
	r := newRig(t, nil)
	for _, h := range []string{"", "Bearer nope"} {
		req, _ := http.NewRequest("GET", r.front.URL+"/models", nil)
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("%q: %d", h, resp.StatusCode)
		}
	}
	r.be.signedOut = true
	if st, _, _ := r.do(t, "GET", "/v1/models", ""); st != 503 {
		t.Fatalf("signed out: %d", st)
	}
}

func TestStreamingPassesThroughAndFallbackOn402(t *testing.T) {
	var primary, alt int32
	altSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&alt, 1)
		if req.Header.Get("X-Extra") != "1" {
			t.Error("provider headers missing on the fallback")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, s := range []string{`data: {"n":1}`, `data: [DONE]`} {
			io.WriteString(w, s+"\n\n")
			w.(http.Flusher).Flush()
		}
	}))
	defer altSrv.Close()
	r := newRig(t, func(w http.ResponseWriter, req *http.Request, _ string) {
		atomic.AddInt32(&primary, 1)
		w.WriteHeader(402)
	})
	r.be.fallback, r.be.headers = altSrv.URL+"/v1", map[string]string{"X-Extra": "1"}
	st, body, hd := r.do(t, "POST", "/chat/completions", `{"model":"real-a","stream":true}`)
	if st != 200 || !strings.Contains(body, "[DONE]") || hd.Get("Content-Type") != "text/event-stream" || primary != 1 || alt != 1 {
		t.Fatalf("fallback: %d %q primary=%d alt=%d", st, body, primary, alt)
	}
	// an explicit empty override disables the fallback
	r.set.Set("fake", "fallback", "")
	if st, _, _ := r.do(t, "POST", "/chat/completions", `{}`); st != 402 {
		t.Fatalf("fallback off: %d", st)
	}
}

func TestRefreshOn401(t *testing.T) {
	var n int32
	r := newRig(t, func(w http.ResponseWriter, req *http.Request, _ string) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, req.Header.Get("Authorization"))
	})
	if st, body, _ := r.do(t, "GET", "/v1/anything", ""); st != 200 || body != "Bearer REFRESHED" {
		t.Fatalf("%d %q", st, body)
	}
}

// Aliases come first in the list, on every spelling, and duplicates of an alias name are dropped.
func TestModelListOrderWithAliases(t *testing.T) {
	r := newRig(t, nil)
	if err := r.set.PutAlias("fake", "fast", "real-b", []string{"real-a", "real-b"}); err != nil {
		t.Fatal(err)
	}
	if err := r.set.PutAlias("fake", "smart", "real-a", []string{"real-a", "real-b"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/models", "/v1/models", "/api/models", "/api/v1/models"} {
		st, body, _ := r.do(t, "GET", p, "")
		if st != 200 {
			t.Fatalf("%s: %d", p, st)
		}
		if got := strings.Join(ids(t, body), ","); got != "fast,smart,real-a,real-b" {
			t.Fatalf("%s order = %s", p, got)
		}
		var l struct {
			Data []map[string]any `json:"data"`
		}
		json.Unmarshal([]byte(body), &l)
		if l.Data[0]["owned_by"] != "x" || l.Data[0]["object"] != "model" {
			t.Fatalf("alias entry should copy its target: %v", l.Data[0])
		}
	}
	// the list was cached for the admin UI
	if c, _, ok := r.proxy.Models.Get("fake"); !ok || strings.Join(c, ",") != "real-a,real-b" {
		t.Fatalf("cache = %v", c)
	}
	// a single model lookup by alias resolves to the target
	r.do(t, "GET", "/v1/models/fast", "")
	if path, _, _, _ := r.last(); path != "/v1/models/real-b" {
		t.Fatalf("lookup path = %s", path)
	}
	// with no aliases the list is untouched
	r.set.DeleteAlias("fake", "fast")
	r.set.DeleteAlias("fake", "smart")
	if _, body, _ := r.do(t, "GET", "/models", ""); body != upModels {
		t.Fatalf("unmodified list expected, got %s", body)
	}
}

// Requests naming an alias go upstream with the target, streaming included; the change applies at
// once; other fields and unrelated bodies are left alone.
func TestAliasRewriteOnEverySpelling(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, req *http.Request, body string) {
		if strings.Contains(body, `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: "+body+"\n\ndata: [DONE]\n\n")
			return
		}
		io.WriteString(w, `{"model":"real-b","ok":true}`)
	})
	real := []string{"real-a", "real-b"}
	r.set.PutAlias("fake", "grok", "real-a", real)
	model := func(body string) string {
		var m struct {
			Model string `json:"model"`
		}
		json.Unmarshal([]byte(body), &m)
		return m.Model
	}
	for _, pre := range []string{"", "/v1", "/api", "/api/v1"} {
		r.do(t, "POST", pre+"/chat/completions", `{"model":"grok","messages":[{"role":"user","content":"hi"}],"temperature":0.2}`)
		_, _, _, body := r.last()
		if model(body) != "real-a" || !strings.Contains(body, `"temperature":0.2`) || !strings.Contains(body, `"messages"`) {
			t.Fatalf("%q: upstream body %s", pre, body)
		}
		_, sbody, _ := r.do(t, "POST", pre+"/responses", `{"model":"grok","stream":true,"input":"x"}`)
		if _, _, _, ub := r.last(); model(ub) != "real-a" || !strings.Contains(sbody, "[DONE]") {
			t.Fatalf("%q stream: %s / %s", pre, ub, sbody)
		}
	}
	// the response is relayed as the provider sent it: the model field names the target
	if _, body, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"grok"}`); model(body) != "real-b" {
		t.Fatalf("response body altered: %s", body)
	}
	// real models, other aliases' names and non-JSON bodies pass through unchanged
	for _, in := range []string{`{"model":"real-b"}`, `{"model":"unknown"}`, `{"input":"x"}`, `not json`, `["grok"]`, `{"model":5}`} {
		r.do(t, "POST", "/v1/chat/completions", in)
		if _, _, _, got := r.last(); got != in {
			t.Errorf("body %q became %q", in, got)
		}
	}
	// changing the mapping applies to the next request
	r.set.PutAlias("fake", "grok", "real-b", real)
	r.do(t, "POST", "/chat/completions", `{"model":"grok"}`)
	if _, _, _, body := r.last(); model(body) != "real-b" {
		t.Fatalf("remap not applied: %s", body)
	}
	r.set.DeleteAlias("fake", "grok")
	r.do(t, "POST", "/chat/completions", `{"model":"grok"}`)
	if _, _, _, body := r.last(); model(body) != "grok" {
		t.Fatalf("deleted alias still rewritten: %s", body)
	}
}

func TestAliasValidation(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	s := Settings{KV: db}
	real := []string{"grok-4", "Grok-Mini"}
	for name, c := range map[string]struct{ alias, target string }{
		"collides with a real id":        {"grok-4", "grok-4"},
		"collides ignoring case":         {"GROK-MINI", "grok-4"},
		"empty":                          {"", "grok-4"},
		"space":                          {"my alias", "grok-4"},
		"slash":                          {"a/b", "grok-4"},
		"too long":                       {strings.Repeat("a", 65), "grok-4"},
		"leading dash":                   {"-x", "grok-4"},
		"target is not a provider model": {"fast", "gone"},
	} {
		if err := s.PutAlias("p", c.alias, c.target, real); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := s.PutAlias("p", "fast", "grok-4", nil); err == nil {
		t.Error("no model list: accepted")
	}
	if len(s.Aliases("p")) != 0 {
		t.Fatalf("rejected aliases were stored: %v", s.Aliases("p"))
	}
	if err := s.PutAlias("p", "grok", "grok-4", real); err != nil {
		t.Fatal(err)
	}
	// stored per provider
	if len(s.Aliases("other")) != 0 || len(s.Aliases("p")) != 1 {
		t.Fatal("aliases must be per provider")
	}
}

func TestStaleTargetIssue(t *testing.T) {
	a := Alias{Name: "grok", Target: "grok-4.7"}
	if AliasIssue(a, []string{"grok-4.7"}, true) != "" || AliasIssue(a, nil, false) != "" {
		t.Fatal("no issue expected")
	}
	if msg := AliasIssue(a, []string{"grok-5"}, true); !strings.Contains(msg, "grok-4.7") {
		t.Fatalf("stale target not reported: %q", msg)
	}
	if msg := AliasIssue(a, []string{"grok-4.7", "GROK"}, true); msg == "" {
		t.Fatal("a real model taking the alias name must be reported")
	}
}

func TestLegacySettingsMigrate(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer db.Close()
	db.SetSetting("upstream_base", "https://a.example/v1")
	db.SetSetting("upstream_fallback", "")
	s := Settings{KV: db}
	s.MigrateLegacy("fake")
	be := &fakeBackend{base: "https://default.example/v1", fallback: "https://fb.example/v1"}
	if s.Base(be) != "https://a.example/v1" {
		t.Fatalf("base = %s", s.Base(be))
	}
	if v, ok := s.Get("fake", "fallback"); !ok || v != "" {
		t.Fatal("explicitly disabled fallback must survive")
	}
	if _, ok := db.GetSetting("upstream_base"); ok {
		t.Fatal("legacy key left")
	}
}
