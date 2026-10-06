package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// second is a key-based provider behind the same proxy.
type second struct {
	fakeBackend
	id      string
	ready   bool
	refresh int32
}

func (s *second) ID() string      { return s.id }
func (s *second) Ready() bool     { return s.ready }
func (s *second) StaticKey() bool { return true }
func (s *second) ForceRefresh(context.Context) (string, error) {
	atomic.AddInt32(&s.refresh, 1)
	return s.token, nil
}

// withSecond adds a second provider whose upstream records the last request and answers with handler.
func withSecond(t *testing.T, r *rig, token string, handler http.HandlerFunc) (*second, *struct{ path, auth, body string }) {
	t.Helper()
	got := &struct{ path, auth, body string }{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		got.path, got.auth, got.body = req.URL.Path, req.Header.Get("Authorization"), string(b)
		if handler != nil {
			handler(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(req.URL.Path, "/models") {
			io.WriteString(w, `{"object":"list","data":[{"id":"other-1"},{"id":"other-2"}]}`)
			return
		}
		io.WriteString(w, `{"from":"second"}`)
	}))
	t.Cleanup(up.Close)
	s := &second{fakeBackend: fakeBackend{base: up.URL + "/v1", token: token}, id: "other", ready: true}
	r.proxy.Add(s)
	return s, got
}

func TestAliasRoutesToItsProviderAndModel(t *testing.T) {
	r := newRig(t, nil)
	_, got := withSecond(t, r, "KEY-2", nil)
	if err := r.set.PutAlias("other", "smart", "other-2", []string{"other-2"}); err != nil {
		t.Fatal(err)
	}
	st, body, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"smart","messages":[]}`)
	if st != 200 || !strings.Contains(body, "second") {
		t.Fatalf("got %d %s", st, body)
	}
	if got.auth != "Bearer KEY-2" || !strings.Contains(got.body, `"model":"other-2"`) {
		t.Fatalf("upstream saw %+v", got)
	}
	if _, _, auth, _ := r.last(); auth != "" {
		t.Fatalf("the first provider must not see the request: %q", auth)
	}
}

func TestPrefixedModelIsStrippedBeforeTheProvider(t *testing.T) {
	r := newRig(t, nil)
	_, got := withSecond(t, r, "KEY-2", nil)
	if err := r.set.SetPrefix("other", "other"); err != nil {
		t.Fatal(err)
	}
	r.proxy.Models.Set("other", []string{"other-1"})
	st, _, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"other_other-1"}`)
	if st != 200 || !strings.Contains(got.body, `"model":"other-1"`) || strings.Contains(got.body, "other_other-1") {
		t.Fatalf("status %d body %q", st, got.body)
	}
	// the longer prefix wins when one prefix starts another
	if err := r.set.SetPrefix("fake", "ot"); err != nil {
		t.Fatal(err)
	}
	got.body = ""
	r.do(t, "POST", "/v1/chat/completions", `{"model":"other_other-1"}`)
	if !strings.Contains(got.body, `"model":"other-1"`) {
		t.Fatalf("shorter prefix took the request: %q", got.body)
	}
	// a bare id still reaches the provider that lists it
	got.body = ""
	r.do(t, "POST", "/v1/chat/completions", `{"model":"other-1"}`)
	if !strings.Contains(got.body, `"model":"other-1"`) {
		t.Fatalf("bare id: %q", got.body)
	}
	// with the first provider signed out, the model list uses the prefix
	r.be.signedOut = true
	_, body, _ := r.do(t, "GET", "/v1/models", "")
	if !strings.Contains(body, `"other_other-1"`) || strings.Contains(body, `"other-1"`) {
		t.Fatalf("list: %s", body)
	}
}

func TestModelOnlyAnotherProviderListsGoesThere(t *testing.T) {
	r := newRig(t, nil)
	_, got := withSecond(t, r, "KEY-2", nil)
	r.proxy.Models.Set("other", []string{"other-1"})
	r.proxy.Models.Set("fake", []string{"real-a"})
	if st, _, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"other-1"}`); st != 200 || got.path != "/v1/chat/completions" {
		t.Fatalf("status %d, upstream path %q", st, got.path)
	}
	got.path = ""
	r.do(t, "POST", "/v1/chat/completions", `{"model":"real-a"}`)
	if got.path != "" {
		t.Fatal("a Grok model must stay with the first provider")
	}
}

func TestNotReadyProviderIsSkippedAndFirstReadyServes(t *testing.T) {
	r := newRig(t, nil)
	s, got := withSecond(t, r, "KEY-2", nil)
	s.ready = false
	r.proxy.Models.Set("other", []string{"other-1"})
	r.do(t, "POST", "/v1/chat/completions", `{"model":"other-1"}`)
	if got.path != "" {
		t.Fatal("a provider that is not ready must not receive requests")
	}
	// The first provider is signed out: the next ready one takes the unnamed requests.
	r.be.signedOut = true
	s.ready = true
	r.proxy.Models.Set("other", nil)
	if st, body, _ := r.do(t, "POST", "/v1/chat/completions", `{"messages":[]}`); st != 200 || !strings.Contains(body, "second") {
		t.Fatalf("got %d %s", st, body)
	}
}

func TestModelListHasEveryProvidersAliases(t *testing.T) {
	r := newRig(t, nil)
	withSecond(t, r, "KEY-2", nil)
	r.set.PutAlias("fake", "from-first", "real-a", []string{"real-a"})
	r.set.PutAlias("other", "from-other", "other-1", []string{"other-1"})
	_, body, _ := r.do(t, "GET", "/v1/models", "")
	l := ids(t, body)
	if !contains(l, "from-first") || !contains(l, "from-other") || !contains(l, "real-a") {
		t.Fatalf("list: %v", l)
	}
}

func TestKeyProviderGetsNoRefreshRetryAndEmptyKeySendsNoHeader(t *testing.T) {
	r := newRig(t, nil)
	s, got := withSecond(t, r, "", func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(401) })
	r.set.PutAlias("other", "x", "m", []string{"m"})
	if st, _, _ := r.do(t, "POST", "/v1/chat/completions", `{"model":"x"}`); st != 401 {
		t.Fatalf("status %d", st)
	}
	if s.refresh != 0 {
		t.Fatal("a fixed key must not be refreshed")
	}
	if got.auth != "" {
		t.Fatalf("no key means no Authorization header, got %q", got.auth)
	}
}

func TestHelperCallsRouteLikeRequests(t *testing.T) {
	r := newRig(t, nil)
	_, got := withSecond(t, r, "KEY-2", nil)
	r.set.PutAlias("other", "h", "other-2", []string{"other-2"})
	st, _, err := r.proxy.Post(context.Background(), "/chat/completions", []byte(`{"model":"h"}`))
	if err != nil || st != 200 || !strings.Contains(got.body, "other-2") {
		t.Fatalf("%d %v %+v", st, err, got)
	}
	ids, err := r.proxy.FetchModelsOf(context.Background(), "other")
	if err != nil || len(ids) != 2 {
		t.Fatalf("%v %v", ids, err)
	}
	if _, err := r.proxy.FetchModelsOf(context.Background(), "nope"); err == nil {
		t.Fatal("unknown provider must fail")
	}
}
