package app

import (
	"context"
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

// The provider's aliases are in its helper model picker by name, in an Aliases group before the models, with no
// note of their target. Picking one stores the alias; the helper call resolves it at call time, so moving the alias
// moves the helper.
func TestHelperPickerOffersAliasesAndTheHelperFollowsThem(t *testing.T) {
	up, sent := aliasUpstream(t)
	a, _, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"grok-mini"}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"deep"}, "target": {"grok-4.7-reasoning"}})
	for _, path := range []string{"/admin", "/admin/upstreams/new"} {
		_, page := br.get(path)
		for _, p := range pickers(t, page) {
			last := -1
			for _, w := range []string{`<optgroup label="Aliases">`, `<option value="fast">fast</option>`,
				`<option value="deep" data-frontier>deep</option>`, `<optgroup label="Models">`, `<option value="grok-mini">grok-mini</option>`} {
				i := strings.Index(p, w)
				if i < 0 || i < last {
					t.Fatalf("%s: %q missing or out of order in\n%s", path, w, p)
				}
				last = i
			}
			if strings.Contains(p, "(alias of") || strings.Contains(p, "Your aliases") {
				t.Errorf("%s: an alias carries an extra label", path)
			}
		}
	}

	resp, body := postJSON(br, "/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"fast"}})
	if resp.StatusCode != 200 || !strings.Contains(body, "MCP helper model: fast") {
		t.Fatalf("picking an alias: %d %s", resp.StatusCode, body)
	}
	if m := a.Admin.Set.Model("grok"); m != "fast" {
		t.Fatalf("stored %q, want the alias", m)
	}
	_, page := br.get("/admin")
	for _, p := range pickers(t, page) {
		opts := optionRE.FindAllStringSubmatch(p, -1)
		if opts[0][1] != "" {
			t.Errorf("an alias of this provider is shown at the top as another provider's model: %q", opts[0][0])
		}
		if !strings.Contains(p, `<option value="fast" selected>fast</option>`) {
			t.Errorf("the chosen alias is not selected in the Aliases group:\n%s", p)
		}
	}
	if strings.Contains(page, "no longer in the provider's model list") {
		t.Error("an alias as the helper is flagged as gone")
	}

	ctx := context.Background()
	call := func() {
		body := []byte(`{"model":"` + a.Admin.Set.Model("grok") + `","messages":[]}`)
		if _, _, err := a.Proxy.Post(ctx, "/chat/completions", body); err != nil {
			t.Fatal(err)
		}
		resp, err := a.Proxy.Stream(ctx, "/chat/completions", body)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	call()
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"plain"}})
	call()
	if got := strings.Join(sent(), ","); got != "grok-mini,grok-mini,plain,plain" {
		t.Errorf("models sent for the helper: %s, want the alias target at each call", got)
	}
	if m := a.Admin.Set.Model("grok"); m != "fast" {
		t.Errorf("moving the alias changed the stored helper to %q", m)
	}
}

// An alias of a key-based provider: its own picker lists it by name, the Grok picker shows it at the top as just the
// alias, and the helper call goes to that provider with the alias target.
func TestHelperAliasOfAnotherProvider(t *testing.T) {
	grokUp, _ := aliasUpstream(t)
	oa, last := openAIUpstream(t, "gpt-x", "gpt-y")
	a, _, br, csrf, _ := signedInProvider(t, grokUp)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	addProvider(br, csrf, "openai", oa.URL+"/v1", "sk-live")
	br.post("/admin/providers/openai/aliases/put", url.Values{"csrf": {csrf}, "name": {"writer"}, "target": {"openai_gpt-y"}})
	_, page := br.get("/admin")
	dlg := page[strings.Index(page, `id="provider-openai"`):]
	dlg = dlg[:strings.Index(dlg, "</template>")]
	if p := pickers(t, dlg)[0]; !strings.Contains(p, `<optgroup label="Aliases"><option value="writer">writer</option>`) {
		t.Fatalf("the OpenAI picker lacks its alias:\n%s", p)
	}
	if resp, body := postJSON(br, "/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"writer"}}); !strings.Contains(body, "MCP helper model: writer") {
		t.Fatalf("picking another provider's alias: %d %s", resp.StatusCode, body)
	}
	_, page = br.get("/admin")
	grok := page[strings.Index(page, `id="provider-grok"`):]
	grok = grok[:strings.Index(grok, "</template>")]
	if opts := optionRE.FindAllStringSubmatch(pickers(t, grok)[0], -1); opts[0][0] != `<option value="writer" selected>writer</option>` || opts[1][2] != "none" {
		t.Errorf("the Grok picker shows another provider's alias as %q, then %q", opts[0][0], opts[1][0])
	}
	if _, _, err := a.Proxy.Post(context.Background(), "/chat/completions", []byte(`{"model":"`+a.Admin.Set.Model("grok")+`","messages":[]}`)); err != nil {
		t.Fatal(err)
	}
	if _, body := last(); !strings.Contains(body, `"model":"gpt-y"`) {
		t.Errorf("the helper call reached OpenAI as %s", body)
	}
}

// An alias whose name is a model id of another provider stays the alias when it is picked: it is not turned into
// that provider's prefixed model.
func TestHelperAliasNamedLikeAnotherProvidersModel(t *testing.T) {
	grokUp, sent := aliasUpstream(t)
	oa, _ := openAIUpstream(t, "gpt-x")
	a, _, br, csrf, _ := signedInProvider(t, grokUp)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	addProvider(br, csrf, "openai", oa.URL+"/v1", "sk-live")
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"gpt-x"}, "target": {"grok-mini"}})
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"gpt-x"}})
	if m := a.Admin.Set.Model("grok"); m != "gpt-x" {
		t.Fatalf("stored %q, want the alias gpt-x", m)
	}
	if _, _, err := a.Proxy.Post(context.Background(), "/chat/completions", []byte(`{"model":"gpt-x","messages":[]}`)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sent(), ","); got != "grok-mini" {
		t.Errorf("the alias reached Grok as %q", got)
	}
}

// In a real browser (optional, like the layout sweep): the helper model picker in the provider Details, the
// helper dialog on /admin and on the upstream form has no blank entry, lists the alias, and saves it when picked.
func TestHelperPickerAliasesInBrowser(t *testing.T) {
	up, _ := aliasUpstream(t)
	_, ts, br, csrf, _ := signedInProvider(t, up)
	postJSON(br, "/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"grok-mini"}})
	runBrowserScript(t, "helperaliases.js", ts.URL, br, "fast", "/admin#provider-grok", "/admin#helper-model", "/admin/upstreams/new#helper-model")
}
