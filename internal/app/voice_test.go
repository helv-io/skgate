package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every admin template, the page script and the admin strings use the plain voice of the app: no filler words, no
// em dashes, no parentheses in labels or hints, no protocol jargon in hints, and hints of one short sentence.
func TestAdminTemplatesUseThePlainVoice(t *testing.T) {
	files, err := filepath.Glob("../admin/templates/*.html")
	if err != nil || len(files) < 10 {
		t.Fatalf("templates: %v %d", err, len(files))
	}
	comment := regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)
	action := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	tags := regexp.MustCompile(`<[^>]+>`)
	hint := regexp.MustCompile(`(?s)<(?:p|ul|li) class="muted"[^>]*>(.*?)</(?:p|ul|li)>`)
	label := regexp.MustCompile(`<(?:label|option|summary)[^>]*>([^<]*)`)
	filler := []string{"leverage", "seamless", "powerful", "helpful", "Let's", "let's", "simply", "easily", "\u2014", "\u2013"}
	jargon := []string{"DCR", "PKCE", "X-API-Key", "self-registered", "newest 500"}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		src := comment.ReplaceAllString(string(b), "")
		for _, w := range append(append([]string{}, filler...), jargon...) {
			if strings.Contains(src, w) {
				t.Errorf("%s: %q does not belong in the copy", name, w)
			}
		}
		text := action.ReplaceAllString(src, "")
		for _, m := range hint.FindAllStringSubmatch(text, -1) {
			txt := strings.Join(strings.Fields(tags.ReplaceAllString(m[1], "")), " ")
			if strings.Contains(txt, "(") || len(txt) > 100 || strings.Contains(txt, ". ") {
				t.Errorf("%s: hint is not short and plain: %q", name, txt)
			}
		}
		for _, m := range label.FindAllStringSubmatch(text, -1) {
			if strings.Contains(m[1], "(") {
				t.Errorf("%s: a label with a parenthesis: %q", name, strings.TrimSpace(m[1]))
			}
		}
	}
	scripts := []string{"../admin/static/app.js"}
	more, _ := filepath.Glob("../admin/*.go")
	for _, f := range more {
		if !strings.HasSuffix(f, "_test.go") {
			scripts = append(scripts, f)
		}
	}
	for _, f := range scripts {
		for i, l := range strings.Split(readFile(t, f), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "//") {
				continue
			}
			for _, w := range filler {
				if strings.Contains(l, w) {
					t.Errorf("%s:%d: %q does not belong in the copy", filepath.Base(f), i+1, w)
				}
			}
		}
	}
}
