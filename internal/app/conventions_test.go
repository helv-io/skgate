package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/config"
)

// The version is shown once, as semver from the build, in the top bar.
func TestVersionShownOnceAsSemver(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(config.Version) {
		t.Fatalf("Version %q is not plain semver", config.Version)
	}
	_, _, br, _ := signedIn(t, nil)
	for _, p := range []string{"/admin", "/admin/keys", "/admin/upstreams", "/admin/clients"} {
		_, page := br.get(p)
		if n := strings.Count(page, config.Version); n != 1 {
			t.Errorf("%s shows the version %d times", p, n)
		}
	}
}

// Status pills carry details in their hover text: no text sits beside or beneath a pill.
func TestNoSubtitleBesidePills(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "admin", "templates", "*.html"))
	beside := regexp.MustCompile(`class="pill[^"]*"[^>]*>[^<]*</span>\s*<span class="(muted|sub)"`)
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if beside.Match(b) {
			t.Errorf("%s puts text beside a pill; use the pill's hover text", f)
		}
		if strings.Contains(string(b), `<span class="pill`) && !strings.HasSuffix(f, "components.html") {
			t.Errorf("%s writes a pill by hand; use the shared pill component", f)
		}
	}
}

// AGENT.md carries the product principle and the README points to it.
func TestAgentDocAndReadmePointer(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "AGENT.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Product principle", "out of the box", "not security experts", "adapt automatically", "PKCE", "Minimize per-client and per-upstream settings",
		"public clients", "5 s", "hover tooltip", "uid 1000", "stack YAML", "semver", "go test -race", "chk.sh", "Authentik", "never sent to a model"} {
		if !strings.Contains(string(b), want) && !strings.Contains(strings.ToLower(string(b)), strings.ToLower(want)) {
			t.Errorf("AGENT.md lacks %q", want)
		}
	}
	r, _ := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if !strings.Contains(string(r), "AGENT.md") {
		t.Error("README does not point to AGENT.md")
	}
}

// Icons are served without login at fixed paths, from the files in internal/admin/static, and the
// admin pages link them.
func TestFaviconRoutes(t *testing.T) {
	_, ts, br, _ := signedIn(t, nil)
	for path, ct := range map[string]string{"/favicon.svg": "image/svg+xml", "/favicon.ico": "image/", "/apple-touch-icon.png": "image/png"} {
		resp, err := http.Get(ts.URL + path) // no session
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ct) || len(b) < 100 {
			t.Errorf("%s: %d %q %d bytes", path, resp.StatusCode, resp.Header.Get("Content-Type"), len(b))
		}
		on, _ := os.ReadFile(filepath.Join("..", "admin", "static", path[1:]))
		if string(on) != string(b) {
			t.Errorf("%s is not the file in static/", path)
		}
	}
	if !strings.HasPrefix(string(mustGet(t, ts.URL+"/favicon.svg")), "<svg") {
		t.Error("svg body")
	}
	_, page := br.get("/admin")
	for _, want := range []string{`rel="icon" href="/favicon.svg" type="image/svg+xml"`, `rel="icon" href="/favicon.ico"`, `rel="apple-touch-icon" href="/apple-touch-icon.png"`, `<span class="brand"><img src="/favicon.svg"`} {
		if !strings.Contains(page, want) {
			t.Errorf("admin layout lacks %q", want)
		}
	}
	// nothing else moved
	for _, p := range []string{"/healthz", "/.well-known/oauth-authorization-server"} {
		if resp, err := http.Get(ts.URL + p); err != nil || resp.StatusCode != 200 {
			t.Errorf("%s shadowed", p)
		}
	}
	if resp, _ := http.Post(ts.URL+"/favicon.ico", "text/plain", nil); resp.StatusCode == 200 {
		t.Error("POST must not be served")
	}
}

func mustGet(t *testing.T, u string) []byte {
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}
