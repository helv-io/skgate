package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/openapi"
)

// checkAPI answers each path with a status and records what it was asked.
type checkAPI struct {
	*httptest.Server
	mu    sync.Mutex
	seen  []string
	auths []string
}

func newCheckAPI(t *testing.T, answer func(r *http.Request) int) *checkAPI {
	c := &checkAPI{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.seen = append(c.seen, r.Method+" "+r.URL.Path)
		c.auths = append(c.auths, r.Header.Get("Authorization"))
		c.mu.Unlock()
		st := answer(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		w.Write([]byte(`[]`))
	}))
	t.Cleanup(c.Close)
	return c
}

// draft checks an unsaved OpenAPI upstream the way Add does.
func draft(t *testing.T, e *env, base, spec, kind, key string, enabled ...string) TestResult {
	t.Helper()
	sel := openapi.Selection{Enabled: map[string]bool{}}
	for _, k := range enabled {
		sel.Enabled[k] = true
	}
	return e.srv.TestOpenAPIDraft(t.Context(), Upstream{Alias: "d", Kind: KindOpenAPI, URL: base, AuthKind: kind, AuthValue: key, Enabled: true}, OpenAPIConfig{Spec: spec, Selection: sel})
}

const checkSpec = `{"openapi":"3.0.0","info":{"title":"T","version":"1"},"security":[{"b":[]}],
"components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}},
"paths":{"/about":{"get":{"operationId":"about","security":[]}},"/things":{"get":{"operationId":"things"}},
"/things/{id}":{"get":{"operationId":"thing","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]}},
"/make":{"post":{"operationId":"make"}}}}`

func TestCheckAcceptsA2xxAndPrefersAProtectedOperation(t *testing.T) {
	e := newEnv(t, nil)
	api := newCheckAPI(t, func(r *http.Request) int { return 200 })
	tr := draft(t, e, api.URL, checkSpec, AuthAuto, "KEY", "GET /about", "GET /things")
	if !tr.OK || tr.Error != "" {
		t.Fatalf("%+v", tr)
	}
	if len(api.seen) != 1 || api.seen[0] != "GET /things" || api.auths[0] != "Bearer KEY" {
		t.Errorf("the protected read comes first, with the key: %v %v", api.seen, api.auths)
	}
}

func TestCheckRefusedKeyAndMissingKeyBlock(t *testing.T) {
	e := newEnv(t, nil)
	api := newCheckAPI(t, func(r *http.Request) int { return 401 })
	if tr := draft(t, e, api.URL, checkSpec, AuthAuto, "WRONG", "GET /things"); tr.OK || tr.Error != "the server refused the key" {
		t.Errorf("wrong key: %+v", tr)
	}
	if tr := draft(t, e, api.URL, checkSpec, AuthNone, "", "GET /things"); tr.OK || tr.Error != "the server needs a key" {
		t.Errorf("no key: %+v", tr)
	}
}

func TestCheckForbiddenTriesAnotherReadBeforeBlocking(t *testing.T) {
	e := newEnv(t, nil)
	spec := strings.Replace(checkSpec, `"/about":{"get":{"operationId":"about","security":[]}}`, `"/admin":{"get":{"operationId":"admin"}}`, 1)
	api := newCheckAPI(t, func(r *http.Request) int {
		if r.URL.Path == "/admin" {
			return 403 // the key is fine, this one is for admins
		}
		return 200
	})
	tr := draft(t, e, api.URL, spec, AuthAuto, "KEY", "GET /admin", "GET /things")
	if !tr.OK || len(api.seen) != 2 {
		t.Fatalf("%+v saw %v", tr, api.seen)
	}
	allDenied := newCheckAPI(t, func(r *http.Request) int { return 403 })
	if tr := draft(t, e, allDenied.URL, spec, AuthAuto, "KEY", "GET /admin", "GET /things"); tr.OK || tr.Error != "the server refused the key" {
		t.Errorf("every read forbidden: %+v", tr)
	}
}

func TestCheckOtherAnswersAndNoAnswerBlock(t *testing.T) {
	e := newEnv(t, nil)
	notFound := newCheckAPI(t, func(r *http.Request) int { return 404 })
	if tr := draft(t, e, notFound.URL, checkSpec, AuthAuto, "KEY", "GET /things"); tr.OK || !strings.Contains(tr.Error, "not found") || !strings.Contains(tr.Error, "base URL") {
		t.Errorf("404: %+v", tr)
	}
	broken := newCheckAPI(t, func(r *http.Request) int { return 500 })
	if tr := draft(t, e, broken.URL, checkSpec, AuthAuto, "KEY", "GET /things"); tr.OK || tr.Error != "the server answered 500" {
		t.Errorf("500: %+v", tr)
	}
	old := openapi.KeyProbeTimeout
	openapi.KeyProbeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { openapi.KeyProbeTimeout = old })
	slow := newCheckAPI(t, func(r *http.Request) int { time.Sleep(600 * time.Millisecond); return 200 })
	if tr := draft(t, e, slow.URL, checkSpec, AuthAuto, "KEY", "GET /things"); tr.OK || tr.Error != "cannot reach the server" {
		t.Errorf("timeout: %+v", tr)
	}
	slow.Close()
	if tr := draft(t, e, slow.URL, checkSpec, AuthAuto, "KEY", "GET /things"); tr.OK || tr.Error != "cannot reach the server" {
		t.Errorf("closed: %+v", tr)
	}
}

func TestCheckWithoutAParameterlessReadAsksNothing(t *testing.T) {
	e := newEnv(t, nil)
	api := newCheckAPI(t, func(r *http.Request) int { return 401 })
	spec := `{"openapi":"3.0.0","info":{"title":"T","version":"1"},"paths":{"/things/{id}":{"get":{"operationId":"thing","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]}},
"/search":{"get":{"operationId":"search","parameters":[{"name":"q","in":"query","required":true,"schema":{"type":"string"}}]}},"/make":{"post":{"operationId":"make"}},"/gone":{"delete":{"operationId":"gone"}}}}`
	tr := draft(t, e, api.URL, spec, AuthNone, "", "GET /search", "GET /things/{id}")
	if !tr.OK || tr.Error != "" || len(api.seen) != 0 {
		t.Errorf("nothing to check: %+v saw %v", tr, api.seen)
	}
}

func TestCheckNeverSendsAChange(t *testing.T) {
	e := newEnv(t, nil)
	api := newCheckAPI(t, func(r *http.Request) int { return 200 })
	draft(t, e, api.URL, checkSpec, AuthAuto, "KEY", "GET /things", "POST /make")
	for _, s := range api.seen {
		if !strings.HasPrefix(s, "GET ") {
			t.Errorf("the check sent %s", s)
		}
	}
}

func TestCheckFallsBackToAReadThatIsOffWhenNoneIsOn(t *testing.T) {
	e := newEnv(t, nil)
	api := newCheckAPI(t, func(r *http.Request) int { return 401 })
	if tr := draft(t, e, api.URL, checkSpec, AuthAuto, "WRONG"); tr.OK || tr.Error != "the server refused the key" {
		t.Errorf("a large description starts with every tool off: %+v", tr)
	}
}
