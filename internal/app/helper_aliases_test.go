package app

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// pickers returns every helper model <select> on a page, from its tag to its </select>.
func pickers(t *testing.T, page string) []string {
	t.Helper()
	var out []string
	for rest := page; ; {
		i := strings.Index(rest, `<select name="model"`)
		if i < 0 {
			break
		}
		rest = rest[i:]
		j := strings.Index(rest, "</select>")
		out = append(out, rest[:j])
		rest = rest[j:]
	}
	if len(out) == 0 {
		t.Fatal("no helper model picker on the page")
	}
	return out
}

var optionRE = regexp.MustCompile(`<option value="([^"]*)"[^>]*>([^<]*)</option>`)

// The helper model picker has no blank entry: the only empty value is "none", and nothing sits above it unless the
// helper belongs to another provider. Every place that renders the picker shares it.
func TestHelperPickerHasNoBlankEntry(t *testing.T) {
	up, _ := aliasUpstream(t)
	_, _, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	for _, model := range []string{"", "grok-mini"} {
		br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {model}})
		for _, path := range []string{"/admin", "/admin/upstreams/new"} {
			_, page := br.get(path)
			for _, p := range pickers(t, page) {
				opts := optionRE.FindAllStringSubmatch(p, -1)
				if len(opts) == 0 {
					t.Fatalf("%s: empty picker", path)
				}
				if opts[0][1] != "" || opts[0][2] != "none" {
					t.Errorf("%s, helper %q: the first entry is %q %q, want none", path, model, opts[0][1], opts[0][2])
				}
				for _, o := range opts {
					if strings.TrimSpace(o[2]) == "" {
						t.Errorf("%s, helper %q: blank entry %q", path, model, o[0])
					}
				}
				if model == "" && strings.Contains(p, "selected") && !strings.Contains(p, `<option value="" selected>none</option>`) {
					t.Errorf("%s: with no helper, none is not the selected entry", path)
				}
			}
		}
	}
}
