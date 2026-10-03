package app

import (
	"os"
	"strings"
	"testing"
)

func appCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../admin/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The page keeps room for the scrollbar, so opening a dialog does not move everything sideways; an alias in a
// heading keeps its case (headings are uppercase, aliases are case-sensitive).
func TestPageDoesNotShiftAndAliasesKeepTheirCase(t *testing.T) {
	css := appCSS(t)
	for _, want := range []string{"html{scrollbar-gutter:stable}", "h2 code{text-transform:none;letter-spacing:0}"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
}
