package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// skgate says SI (SuperIntelligence), never AI, in its docs and its screens. Names that contain the letters stay
// (OpenAI, xAI, SpaceXAI, Google AI Studio): they are not the word AI on its own.
var wordAI = regexp.MustCompile(`\bAI\b`)

func TestDocsSaySINotAI(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	root, _ := filepath.Glob(filepath.Join("..", "..", "*.md"))
	for _, f := range append(files, root...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "SI naming rule") {
				continue // AGENT.md states the rule itself
			}
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
