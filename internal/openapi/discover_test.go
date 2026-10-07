package openapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const jsonSpec = `{"openapi":"3.0.3","info":{"title":"T","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
const yamlSpec = "openapi: 3.0.3\ninfo:\n  title: Y\n  version: '1'\npaths: {}\n"
const tomlSpec = "openapi = \"3.0.3\"\n[info]\ntitle = \"M\"\nversion = \"1\"\n[paths]\n"
const swaggerSpec = `{"swagger":"2.0","info":{"title":"S","version":"1"},"paths":{}}`

// site serves the given paths (body per path) and 404 for everything else; it records the requests.
func site(t *testing.T, routes map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if body, ok := routes[r.URL.Path]; ok {
			// the content type says nothing about the content: it is sniffed
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestSpecPathsListIsOneListWithoutTml(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range SpecPaths {
		if seen[p] || !strings.HasPrefix(p, "/") || strings.HasSuffix(p, ".tml") {
			t.Errorf("bad or repeated candidate %q", p)
		}
		seen[p] = true
	}
	for _, want := range []string{"/openapi.json", "/openapi.yaml", "/openapi.yml", "/openapi.toml", "/swagger.json", "/swagger.yaml", "/swagger.yml",
		"/api/openapi.json", "/api/openapi.yaml", "/api/swagger.json", "/v1/api-docs", "/v2/api-docs", "/v3/api-docs", "/api-docs",
		"/docs/openapi.json", "/api/docs/openapi.json"} {
		if !seen[want] {
			t.Errorf("candidate %s missing", want)
		}
	}
}

func TestDiscoverEveryCandidate(t *testing.T) {
	for _, p := range SpecPaths {
		body := jsonSpec
		switch {
		case strings.HasSuffix(p, ".yaml"), strings.HasSuffix(p, ".yml"):
			body = yamlSpec
		case strings.HasSuffix(p, ".toml"):
			body = tomlSpec
		}
		srv, _ := site(t, map[string]string{p: body})
		f, err := Discover(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if f.URL != srv.URL+p || f.Direct || f.Base != srv.URL || f.Doc == nil {
			t.Errorf("%s: found %q direct=%v base=%q", p, f.URL, f.Direct, f.Base)
		}
	}
}

func TestDiscoverDirectAddressAndSwagger(t *testing.T) {
	srv, seen := site(t, map[string]string{"/spec": swaggerSpec})
	f, err := Discover(context.Background(), srv.URL+"/spec")
	if err != nil || !f.Direct || f.URL != srv.URL+"/spec" || f.Doc.Source != "swagger 2" {
		t.Fatalf("%+v %v", f, err)
	}
	if len(*seen) != 1 {
		t.Errorf("a description at the address needs one request, saw %v", *seen)
	}
}

func TestDiscoverSniffsContentNotName(t *testing.T) {
	// the JSON error page of a missing route is not a description, a description with an odd name is
	srv, _ := site(t, map[string]string{"/openapi.json": `{"detail":"Not Found"}`, "/swagger.json": `<html>docs</html>`, "/v3/api-docs": jsonSpec})
	f, err := Discover(context.Background(), srv.URL)
	if err != nil || !strings.HasSuffix(f.URL, "/v3/api-docs") {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestDiscoverFromDocumentationPages(t *testing.T) {
	cases := map[string]map[string]string{
		"swagger ui": {"/docs": `<script>SwaggerUIBundle({url: '/custom/api.json', dom_id: '#x'})</script>`, "/custom/api.json": jsonSpec},
		"redoc":      {"/redoc": `<redoc spec-url="/internal/spec.yaml"></redoc>`, "/internal/spec.yaml": yamlSpec},
		"link tag":   {"/": `<html><head><link rel="service-desc" href="/x/desc" type="application/json"></head></html>`, "/x/desc": jsonSpec},
		"link type":  {"/": `<link rel="alternate" type="application/vnd.oai.openapi+json" href="/y">`, "/y": jsonSpec},
		"relative":   {"/api/docs": `<script>const ui = SwaggerUIBundle({ url: "openapi-v2.json" })</script>`, "/api/openapi-v2.json": jsonSpec},
		"initializer": {"/swagger": `<script src="./swagger-initializer.js"></script>`, "/swagger-initializer.js": `window.ui = SwaggerUIBundle({url: "/z.json"})`,
			"/z.json": jsonSpec},
	}
	for name, routes := range cases {
		srv, _ := site(t, routes)
		f, err := Discover(context.Background(), srv.URL)
		if err != nil || f.Doc == nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDiscoverEnteredPageIsReadForLinks(t *testing.T) {
	srv, _ := site(t, map[string]string{"/app/help": `<script>SwaggerUIBundle({url: "/app/schema"})</script>`, "/app/schema": jsonSpec})
	f, err := Discover(context.Background(), srv.URL+"/app/help")
	if err != nil || !strings.HasSuffix(f.URL, "/app/schema") {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestDiscoverPathPrefix(t *testing.T) {
	srv, _ := site(t, map[string]string{"/api/v1/openapi.json": jsonSpec})
	f, err := Discover(context.Background(), srv.URL+"/api/v1")
	if err != nil || f.URL != srv.URL+"/api/v1/openapi.json" || f.Base != srv.URL+"/api/v1" {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestDiscoverBareHost(t *testing.T) {
	srv, _ := site(t, map[string]string{"/openapi.json": jsonSpec})
	f, err := Discover(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
	if err != nil || f.URL != srv.URL+"/openapi.json" {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestDiscoverNothingAndUnreachable(t *testing.T) {
	srv, seen := site(t, map[string]string{"/": "<html>hello</html>"})
	if _, err := Discover(context.Background(), srv.URL); !errors.Is(err, ErrNoSpec) {
		t.Errorf("nothing: %v", err)
	}
	for _, r := range *seen {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("only GET is sent, saw %s", r)
		}
	}
	srv.Close()
	if _, err := Discover(context.Background(), srv.URL); !errors.Is(err, ErrUnreachable) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestDiscoverStaysOnTheHostEntered(t *testing.T) {
	// the other server is reached by another name (localhost, not 127.0.0.1), so it is another host
	other, otherSeen := site(t, map[string]string{"/openapi.json": jsonSpec, "/x.json": jsonSpec})
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json": // a redirect to another host is not followed
			http.Redirect(w, r, otherURL+"/openapi.json", http.StatusFound)
		case "/docs": // a link to another host is not read
			_, _ = w.Write([]byte(`<script>SwaggerUIBundle({url: "` + otherURL + `/x.json"})</script>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if _, err := Discover(context.Background(), srv.URL); !errors.Is(err, ErrNoSpec) {
		t.Errorf("found something on another host: %v", err)
	}
	if len(*otherSeen) != 0 {
		t.Errorf("the other host was asked: %v", *otherSeen)
	}
}

func TestDiscoverSameHostRedirectIsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			http.Redirect(w, r, "/real/spec.json", http.StatusMovedPermanently)
		case "/real/spec.json":
			_, _ = w.Write([]byte(jsonSpec))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f, err := Discover(context.Background(), srv.URL)
	if err != nil || f.URL != srv.URL+"/real/spec.json" {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestDiscoverRefusesPublicHttpAndBadInput(t *testing.T) {
	for _, in := range []string{"http://api.example.com", "", "a b", "ftp://x", "http://u:p@application"} {
		if _, err := Discover(context.Background(), in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestLinksAreSameHostOnly(t *testing.T) {
	s := newSearch(context.Background(), mustURL(t, "http://application:8080"))
	page := []byte(`SwaggerUIBundle({url: "https://other.example.com/v2/swagger.json"}) <redoc spec-url="/ok.json">
<link rel="service-desc" href="http://other:9000/x">`)
	got := s.links(page, mustURL(t, "http://application:8080/docs"))
	if len(got) != 1 || got[0] != "http://application:8080/ok.json" {
		t.Errorf("links: %v", got)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
