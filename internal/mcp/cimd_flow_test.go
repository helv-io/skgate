package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// docServer serves one client metadata document over TLS and counts the requests. Its address is on
// loopback, which the real dialer refuses, so tests that need the fetch to work swap in the test
// server's own client.
type docServer struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	ctype  string
	cache  string
	body   func(id string) string
	hits   atomic.Int32
	id     string
}

func newDocServer(t *testing.T) *docServer {
	d := &docServer{status: 200, ctype: "application/json"}
	d.body = func(id string) string {
		return `{"client_id":"` + id + `","client_name":"Doc App","redirect_uris":["` + hostedRedirect + `"],"token_endpoint_auth_method":"none"}`
	}
	d.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.hits.Add(1)
		d.mu.Lock()
		status, ctype, cache, body := d.status, d.ctype, d.cache, d.body(d.id)
		d.mu.Unlock()
		switch {
		case r.URL.Path == "/moved.json":
			http.Redirect(w, r, "/client.json", http.StatusFound)
			return
		case r.URL.Path != "/client.json":
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		if cache != "" {
			w.Header().Set("Cache-Control", cache)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(d.Close)
	d.id = d.URL + "/client.json"
	return d
}

func (d *docServer) set(f func(d *docServer)) { d.mu.Lock(); f(d); d.mu.Unlock() }

// trust makes the server under test fetch with the test server's TLS client.
func (e *env) trust(d *docServer) {
	c := d.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	e.srv.cimd.client = c
}

// expire makes the cached copy of id stale, as if its time had passed.
func (e *env) expire(id string) {
	e.srv.cimd.mu.Lock()
	e.srv.cimd.fresh[id] = cimdState{until: time.Now().Add(-time.Second)}
	e.srv.cimd.mu.Unlock()
}

func TestMetadataClientEndToEnd(t *testing.T) {
	e := newEnv(t, nil)
	d := newDocServer(t)
	e.trust(d)

	// discovery says so
	m := readJSON(t, e.do("GET", "/.well-known/oauth-authorization-server", nil, ""))
	if m["client_id_metadata_document_supported"] != true {
		t.Fatalf("discovery: %v", m)
	}

	verifier, challenge := pkce()
	code, loc, st := e.authorizeCode(d.id, hostedRedirect, challenge, "&scope=mcp")
	if st != 302 || code == "" || loc.Host != "hosted.example" {
		t.Fatalf("authorize: %d %v", st, loc)
	}
	if d.hits.Load() != 1 {
		t.Errorf("one fetch expected, got %d", d.hits.Load())
	}
	c, ok := e.srv.Clients.Get(d.id)
	if !ok || c.Source != cimdSource || c.Name != "Doc App" || c.Confidential() || len(c.RedirectURIs) != 1 || c.LastUsed.IsZero() {
		t.Fatalf("stored client: %+v", c)
	}
	// the code is exchanged by the same URL id, as a public client with PKCE
	tok := e.do("POST", "/token", formHdr, form("grant_type", "authorization_code", "client_id", d.id, "code", code, "code_verifier", verifier, "redirect_uri", hostedRedirect))
	if tok.StatusCode != 200 {
		t.Fatalf("token: %d", tok.StatusCode)
	}
	tj := readJSON(t, tok)
	access, _ := tj["access_token"].(string)
	refresh, _ := tj["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("tokens: %v", tj)
	}
	if reason := e.srv.verifyAccessToken(access, "/mcp"); reason != "" {
		t.Errorf("access token of a metadata client: %s", reason)
	}
	if r := e.do("POST", "/token", formHdr, form("grant_type", "refresh_token", "client_id", d.id, "refresh_token", refresh)); r.StatusCode != 200 {
		t.Errorf("refresh: %d", r.StatusCode)
	}
	if d.hits.Load() != 1 {
		t.Errorf("the cache should have served the token calls, fetches=%d", d.hits.Load())
	}

	// a redirect that the document does not list is refused, before login
	if _, _, st := e.authorizeCode(d.id, "https://hosted.example/other", challenge, ""); st != 400 {
		t.Errorf("unlisted redirect: %d", st)
	}
	// no PKCE, no code (a metadata client is public)
	if code, loc, _ := e.authorizeCodeNoPKCE(d.id, hostedRedirect); code != "" || loc == nil || loc.Query().Get("error") == "" {
		t.Errorf("a metadata client needs PKCE: %v", loc)
	}

	// the document changes after its cache time: the stored client follows it, last use is kept
	d.set(func(d *docServer) {
		d.body = func(id string) string {
			return `{"client_id":"` + id + `","client_name":"Renamed","redirect_uris":["https://hosted.example/new"]}`
		}
	})
	e.expire(d.id)
	if _, _, st := e.authorizeCode(d.id, "https://hosted.example/new", challenge, ""); st != 302 {
		t.Fatalf("new redirect after refresh: %d", st)
	}
	c, _ = e.srv.Clients.Get(d.id)
	if c.Name != "Renamed" || c.RedirectURIs[0] != "https://hosted.example/new" || c.LastUsed.IsZero() {
		t.Errorf("refreshed client: %+v", c)
	}
	if _, _, st := e.authorizeCode(d.id, hostedRedirect, challenge, ""); st != 400 {
		t.Errorf("the old redirect is gone: %d", st)
	}

	// the document disappears: the stored copy keeps working, the failure is not retried for a while
	d.set(func(d *docServer) { d.status = 500 })
	e.expire(d.id)
	before := d.hits.Load()
	for i := 0; i < 3; i++ {
		if _, _, st := e.authorizeCode(d.id, "https://hosted.example/new", challenge, ""); st != 302 {
			t.Fatalf("stored copy while the document is down: %d", st)
		}
	}
	if d.hits.Load() != before+1 {
		t.Errorf("a failed fetch must not be repeated at once: %d -> %d", before, d.hits.Load())
	}
}

func TestMetadataClientRefusals(t *testing.T) {
	_, challenge := pkce()
	for name, tc := range map[string]struct {
		set func(d *docServer)
		id  func(d *docServer) string
		log string
	}{
		"wrong client_id": {set: func(d *docServer) {
			d.body = func(string) string {
				return `{"client_id":"https://other.example/client.json","redirect_uris":["` + hostedRedirect + `"]}`
			}
		}, log: "not the URL it was fetched from"},
		"not json":     {set: func(d *docServer) { d.ctype = "text/html" }, log: "not JSON"},
		"not found":    {id: func(d *docServer) string { return d.URL + "/missing.json" }, log: "HTTP 404"},
		"server error": {set: func(d *docServer) { d.status = 500 }, log: "HTTP 500"},
		"redirect":     {id: func(d *docServer) string { return d.URL + "/moved.json" }, log: "redirects"},
		"too big":      {set: func(d *docServer) { d.body = func(string) string { return strings.Repeat(" ", cimdMaxBody+10) } }, log: "larger than"},
		"secret": {set: func(d *docServer) {
			d.body = func(id string) string {
				return `{"client_id":"` + id + `","redirect_uris":["` + hostedRedirect + `"],"client_secret":"x"}`
			}
		}, log: "client secret"},
		"no https redir": {set: func(d *docServer) {
			d.body = func(id string) string { return `{"client_id":"` + id + `","redirect_uris":["http://lan.example/cb"]}` }
		}, log: "usable"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			d := newDocServer(t)
			e.trust(d)
			if tc.set != nil {
				d.set(tc.set)
			}
			id := d.id
			if tc.id != nil {
				id = tc.id(d)
			}
			if _, _, st := e.authorizeCode(id, hostedRedirect, challenge, ""); st != 400 {
				t.Fatalf("authorize: %d", st)
			}
			if _, ok := e.srv.Clients.Get(id); ok {
				t.Error("a refused document must not leave a client behind")
			}
			if !strings.Contains(e.logs.String(), tc.log) {
				t.Errorf("log lacks %q:\n%s", tc.log, e.logs.String())
			}
			if name == "redirect" && d.hits.Load() != 1 {
				t.Errorf("the redirect target must not be fetched: %d", d.hits.Load())
			}
			// the failure is remembered, so a second attempt does not fetch again
			n := d.hits.Load()
			e.authorizeCode(id, hostedRedirect, challenge, "")
			if d.hits.Load() != n {
				t.Errorf("refused again with a new fetch: %d -> %d", n, d.hits.Load())
			}
		})
	}
}

// With the real dialer, an address that is not public is never contacted, whatever the document says.
func TestMetadataClientNeverReachesNonPublicHosts(t *testing.T) {
	e := newEnv(t, nil)
	d := newDocServer(t) // listens on 127.0.0.1
	_, challenge := pkce()
	if _, _, st := e.authorizeCode(d.id, hostedRedirect, challenge, ""); st != 400 {
		t.Fatalf("authorize: %d", st)
	}
	if d.hits.Load() != 0 {
		t.Fatalf("the loopback server was contacted %d times", d.hits.Load())
	}
	if !strings.Contains(e.logs.String(), "non-public address") {
		t.Errorf("log: %s", e.logs.String())
	}
	u, _ := url.Parse(d.URL)
	for _, id := range []string{"https://localhost:" + u.Port() + "/client.json", "https://[::1]:" + u.Port() + "/client.json", "https://169.254.169.254/latest/client.json", "https://10.0.0.1/client.json"} {
		if _, _, st := e.authorizeCode(id, hostedRedirect, challenge, ""); st != 400 {
			t.Errorf("%s: %d", id, st)
		}
	}
	if d.hits.Load() != 0 {
		t.Fatal("contacted")
	}
}

func TestOtherClientsWithURLIDsAreNotFetched(t *testing.T) {
	e := newEnv(t, nil)
	d := newDocServer(t)
	e.trust(d)
	id := d.URL + "/client.json"
	if _, err := e.srv.Clients.Create(Client{ID: id, Name: "manual", RedirectURIs: []string{hostedRedirect}, AuthMethod: "none", Source: "admin"}, ""); err != nil {
		t.Fatal(err)
	}
	_, challenge := pkce()
	if _, _, st := e.authorizeCode(id, hostedRedirect, challenge, ""); st != 302 {
		t.Fatalf("admin client: %d", st)
	}
	if d.hits.Load() != 0 {
		t.Error("an admin client with an https ID must not be fetched or replaced")
	}
	c, _ := e.srv.Clients.Get(id)
	if c.Source != "admin" || c.Name != "manual" {
		t.Errorf("overwritten: %+v", c)
	}
	if ClientHost(c) != "" {
		t.Error("only metadata clients show a client host")
	}
}

func TestOnlyTheNewestMetadataClientsAreKept(t *testing.T) {
	e := newEnv(t, nil)
	for i := 0; i < cimdKeep+5; i++ {
		id := fmt.Sprintf("https://app%d.example/client.json", i)
		if err := e.srv.Clients.PutMetadataClient(Client{ID: id, Name: "n", RedirectURIs: []string{hostedRedirect}}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := e.srv.Clients.List()
	n := 0
	for _, c := range list {
		if c.Source == cimdSource {
			n++
		}
	}
	if n != cimdKeep {
		t.Errorf("kept %d", n)
	}
	if _, ok := e.srv.Clients.Get("https://app0.example/client.json"); ok {
		t.Error("the oldest should be gone")
	}
	if ClientHost(Client{ID: "https://app9.example/client.json", Source: cimdSource}) != "app9.example" {
		t.Error("client host")
	}
}

// Many requests for the same new client share one fetch.
func TestMetadataClientFetchIsSharedAndBounded(t *testing.T) {
	e := newEnv(t, nil)
	d := newDocServer(t)
	e.trust(d)
	_, challenge := pkce()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.authorizeCode(d.id, hostedRedirect, challenge, "")
		}()
	}
	wg.Wait()
	if n := d.hits.Load(); n != 1 {
		t.Errorf("12 concurrent requests fetched %d times", n)
	}
}
