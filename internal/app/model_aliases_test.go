package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// aliasUpstream serves a plain /models list and, when rich is set, a /language-models list that carries aliases.
func aliasUpstream(t *testing.T, rich, inline bool) *httptest.Server {
	models := func(withAliases bool) any {
		mk := func(id string, al ...string) map[string]any {
			m := map[string]any{"id": id, "object": "model"}
			if withAliases && len(al) > 0 {
				m["aliases"] = al
			}
			return m
		}
		return map[string]any{"object": "list", "data": []any{
			mk("grok-4.7-reasoning", "grok-4.7", "grok-latest", "grok-4.7-reasoning-with-an-unreasonably-long-alias-identifier-0123456789"), mk("grok-mini", "grok-mini"), mk("plain")}}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(models(inline))
		case "/v1/language-models":
			if !rich {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(models(true))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// The helper picker lists a provider's own aliases right after the model they select, stores a chosen alias as it
// is, and does not change anything for a provider that has none.
func TestHelperPickerListsProviderAliases(t *testing.T) {
	for _, mode := range []struct {
		name         string
		rich, inline bool
	}{{"language-models", true, false}, {"inline", false, true}} {
		t.Run(mode.name, func(t *testing.T) {
			a, _, br, csrf, _ := signedInProvider(t, aliasUpstream(t, mode.rich, mode.inline))
			br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
			_, page := br.get("/admin")
			// the alias that equals a model id is not listed twice; the frontier hint follows the target model
			want := []string{`<option value="grok-4.7-reasoning" data-frontier>grok-4.7-reasoning</option>`,
				`<option value="grok-4.7" data-frontier>grok-4.7 (alias of grok-4.7-reasoning)</option>`,
				`<option value="grok-latest" data-frontier>grok-latest (alias of grok-4.7-reasoning)</option>`,
				`<option value="grok-mini">grok-mini</option>`, `<option value="plain">plain</option>`, "3 models, 3 aliases"}
			last := -1
			for _, w := range want {
				i := strings.Index(page, w)
				if i < 0 {
					t.Fatalf("picker lacks %q", w)
				}
				if i < last && !strings.HasPrefix(w, "3 models") {
					t.Errorf("%q is out of order", w)
				}
				last = i
			}
			if strings.Contains(page, "grok-mini (alias of") {
				t.Error("an alias equal to a model id must not be listed again")
			}

			_, body := br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-latest"}})
			if m := a.Admin.Set.Model("grok"); m != "grok-latest" {
				t.Fatalf("stored %q, want the alias as chosen (%s)", m, body)
			}
			_, page = br.get("/admin")
			if !strings.Contains(page, `<option value="grok-latest" data-frontier selected>`) || strings.Contains(page, "(unlisted)") {
				t.Error("the chosen alias must be shown selected and not as unlisted")
			}
			if !strings.Contains(page, `data-frontier-hint="600" >`) && !strings.Contains(page, `data-frontier-hint="600">`) {
				t.Error("the heavy-model suggestion follows the alias's target")
			}
			if strings.Contains(page, "no longer in the provider's model list") {
				t.Error("a listed alias must not be flagged as gone")
			}
			br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"nonsense"}})
			if m := a.Admin.Set.Model("grok"); m != "grok-latest" {
				t.Errorf("an unknown name replaced the model: %q", m)
			}
		})
	}
}

// Without aliases the picker is exactly the model list.
func TestHelperPickerWithoutAliases(t *testing.T) {
	_, _, br, csrf, _ := signedInProvider(t, aliasUpstream(t, false, false))
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	_, page := br.get("/admin")
	if strings.Contains(page, "(alias of") || strings.Contains(page, " aliases, loaded") || !strings.Contains(page, "3 models, loaded") {
		t.Error("no aliases means no alias entries and no alias count")
	}
}
