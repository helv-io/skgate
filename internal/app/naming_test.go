package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// skgate says SI (SuperIntelligence), not AI, in the text that is entirely its own: docs and screens. This checks
// text only. Company and product names (OpenAI, xAI, SpaceXAI, Google AI Studio), URLs, identifiers, JSON keys and
// other external names keep AI; the list is in AGENT.md.
var wordAI = regexp.MustCompile(`\bAI\b`)

func TestDocsSaySINotAI(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	root, _ := filepath.Glob(filepath.Join("..", "..", "*.md"))
	for _, f := range append(files, root...) {
		if filepath.Base(f) == "AGENT.md" {
			continue // it states the rule and lists where AI stays
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if wordAI.MatchString(strings.ReplaceAll(line, "Google AI Studio", "")) {
				t.Errorf("%s:%d says AI, use SI: %.80s", filepath.Base(f), i+1, line)
			}
		}
	}
}

func TestScreensSaySINotAI(t *testing.T) {
	_, _, br, _ := signedIn(t, nil)
	tags := regexp.MustCompile(`<[^>]+>`)
	for _, path := range []string{"/admin", "/admin/keys", "/admin/clients", "/admin/upstreams", "/admin/upstreams/import", "/admin/upstreams/new"} {
		_, page := br.get(path)
		if wordAI.MatchString(strings.ReplaceAll(tags.ReplaceAllString(page, " "), "Google AI Studio", "")) {
			t.Errorf("%s says AI, use SI", path)
		}
	}
}
