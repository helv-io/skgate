package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// .env.example must document every environment variable the code reads, and nothing else.
func TestEnvExampleCoversEveryVariable(t *testing.T) {
	root := filepath.Join("..", "..")
	ex, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^#?\s*([A-Z][A-Z0-9_]+)=`).FindAllStringSubmatch(string(ex), -1) {
		documented[m[1]] = true
	}
	used := map[string]bool{}
	re := regexp.MustCompile(`(?:env|Getenv|LookupEnv|intEnv|durEnv|boolEnv|truthy\(os\.Getenv|splitList\(os\.Getenv|parseID)\(\s*"([A-Z][A-Z0-9_]+)"`)
	for _, dir := range []string{"internal", "cmd"} {
		filepath.Walk(filepath.Join(root, dir), func(p string, fi os.FileInfo, _ error) error {
			// fakemcp is a test-only helper; its FAKE_* switches are not configuration. legacyenv.go only reads
			// variables that were removed, to migrate them.
			if fi == nil || fi.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(filepath.ToSlash(p), "/fakemcp/") || strings.HasSuffix(p, "legacyenv.go") {
				return nil
			}
			b, _ := os.ReadFile(p)
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				used[m[1]] = true
			}
			return nil
		})
	}
	if len(used) < 15 {
		t.Fatalf("env scan found only %d variables: %v", len(used), used)
	}
	for v := range used {
		if !documented[v] {
			t.Errorf("%s is read by the code but missing from .env.example", v)
		}
	}
	for v := range documented {
		if !used[v] {
			t.Errorf("%s is in .env.example but never read", v)
		}
	}
	// The banned strings are split so this file does not contain them.
	for _, bad := range []string{"helv", "Pedre" + "schi", "pedre" + "schi"} {
		if strings.Contains(string(ex), bad) {
			t.Errorf(".env.example contains house-specific %q", bad)
		}
	}
}

// Docs stay generic, mention both image tags, and point at .env.example.
func TestREADMEIsGenericAndReferencesEnvExample(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"helv" + ".io", "helv" + "io", "Pedre" + "schi", "kom" + "odo", "Kom" + "odo", "baby" + "buddy"} {
		if strings.Contains(s, bad) {
			t.Errorf("README contains house-specific %q", bad)
		}
	}
	for _, want := range []string{".env.example", "auth.example.com", "Authentik", "Keycloak", "`latest`", "`slim`", "image: ghcr.io/helv-io/skgate:", "## Quick start", "## Configuration", "## OIDC setup", "## Security notes", "## Development"} {
		if !strings.Contains(s, want) {
			t.Errorf("README lacks %q", want)
		}
	}
	for v := range map[string]bool{"PUBLIC_URL": true, "LISTEN_ADDR": true, "DB_PATH": true, "LOG_LEVEL": true, "PUID": true, "PGID": true, "OIDC_ISSUER": true} {
		if !strings.Contains(s, "`"+v) {
			t.Errorf("README config table lacks %s", v)
		}
	}
}

// Consent is required unless explicitly switched off.
func TestConsentDefaultsToRequired(t *testing.T) {
	for val, want := range map[string]bool{"": true, "true": true, "garbage": true, "1": true, "false": false, "0": false, "off": false, "NO": false} {
		t.Setenv("MCP_OAUTH_REQUIRE_CONSENT", val)
		if got := Load().RequireConsent; got != want {
			t.Errorf("MCP_OAUTH_REQUIRE_CONSENT=%q: RequireConsent=%v, want %v", val, got, want)
		}
	}
}

// Examples and placeholders name no upstream application: hosts read application:8080 and the like.
func TestExamplesNameNoApplication(t *testing.T) {
	root := filepath.Join("..", "..")
	files := []string{"README.md", "CHANGELOG.md", filepath.Join("internal", "admin", "static", "app.js")}
	for _, g := range []string{filepath.Join("docs", "*.md"), filepath.Join("internal", "admin", "templates", "*.html")} {
		m, _ := filepath.Glob(filepath.Join(root, g))
		for _, p := range m {
			r, _ := filepath.Rel(root, p)
			files = append(files, r)
		}
	}
	if len(files) < 10 {
		t.Fatalf("found only %d files", len(files))
	}
	// Split so this file does not contain them.
	bad := []string{"mea" + "lie", "placeholder=\"Home " + "Assistant\""}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range bad {
			if strings.Contains(strings.ToLower(string(b)), strings.ToLower(w)) {
				t.Errorf("%s names an application in an example: %q", f, w)
			}
		}
	}
}
