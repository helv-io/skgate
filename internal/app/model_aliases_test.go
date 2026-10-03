package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// aliasUpstream serves a model list and records the model of every chat completion it receives. The provider side
// reports its own aliases in both list styles; skgate must ignore them.
func aliasUpstream(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var got []string
	mk := func(id string, al ...string) map[string]any {
		m := map[string]any{"id": id, "object": "model"}
		if len(al) > 0 {
			m["aliases"] = al
		}
		return m
	}
	list := map[string]any{"object": "list", "data": []any{mk("grok-4.7-reasoning", "grok-4.7", "upstream-latest"), mk("grok-mini"), mk("plain")}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models", "/v1/language-models":
			json.NewEncoder(w).Encode(list)
		case "/v1/chat/completions":
			b, _ := io.ReadAll(r.Body)
			var req struct {
				Model string `json:"model"`
			}
			json.Unmarshal(b, &req)
			mu.Lock()
			got = append(got, req.Model)
			mu.Unlock()
			w.Write([]byte(`{"choices":[]}`))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

// The helper picker lists the provider's models, then the aliases defined in skgate (Model aliases), never the
// aliases the provider reports itself. A chosen skgate alias is stored as it is, and the helper's call to the
// provider carries the model it resolves to.
func TestHelperPickerListsSkgateAliases(t *testing.T) {
	up, sent := aliasUpstream(t)
	a, _, br, csrf, _ := signedInProvider(t, up)
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"grok-latest"}, "target": {"grok-4.7-reasoning"}})
	br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"fast"}, "target": {"grok-mini"}})
	_, page := br.get("/admin")
	// canonical models first, then the skgate aliases; the frontier hint follows the target model
	want := []string{`<option value="grok-4.7-reasoning" data-frontier>grok-4.7-reasoning</option>`,
		`<option value="grok-mini">grok-mini</option>`, `<option value="plain">plain</option>`,
		`<option value="grok-latest" data-frontier>grok-latest (alias of grok-4.7-reasoning)</option>`,
		`<option value="fast">fast (alias of grok-mini)</option>`, "3 models, loaded"}
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
	for _, gone := range []string{"upstream-latest", `value="grok-4.7"`, " aliases, loaded"} {
		if strings.Contains(page, gone) {
			t.Errorf("an alias reported by the provider must not be offered: %q", gone)
		}
	}

	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-latest"}})
	if m := a.Admin.Set.Model("grok"); m != "grok-latest" {
		t.Fatalf("stored %q, want the alias as chosen", m)
	}
	_, page = br.get("/admin")
	if !strings.Contains(page, `<option value="grok-latest" data-frontier selected>`) || strings.Contains(page, "(unlisted)") {
		t.Error("the chosen alias must be shown selected and not as unlisted")
	}
	if strings.Contains(page, "no longer in the provider's model list") {
		t.Error("a defined alias must not be flagged as gone")
	}
	for _, refused := range []string{"nonsense", "upstream-latest"} {
		br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {refused}})
		if m := a.Admin.Set.Model("grok"); m != "grok-latest" {
			t.Errorf("%q replaced the model: %q", refused, m)
		}
	}

	// the helper's calls (plain and streamed) go out with the real model; a real model id passes untouched
	ctx := context.Background()
	body := func(m string) []byte { return []byte(`{"model":"` + m + `","messages":[]}`) }
	if _, _, err := a.Proxy.Post(ctx, "/chat/completions", body("grok-latest")); err != nil {
		t.Fatal(err)
	}
	resp, err := a.Proxy.Stream(ctx, "/chat/completions", body("fast"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	a.Proxy.Post(ctx, "/chat/completions", body("plain"))
	if got := strings.Join(sent(), ","); got != "grok-4.7-reasoning,grok-mini,plain" {
		t.Errorf("models sent upstream: %s", got)
	}

	// deleting the alias takes it out of the picker again
	br.post("/admin/providers/grok/aliases/delete", url.Values{"csrf": {csrf}, "name": {"fast"}})
	if _, page = br.get("/admin"); strings.Contains(page, "fast (alias of") {
		t.Error("a deleted alias is still offered")
	}
}

// Without skgate aliases the picker is exactly the model list, whatever the provider reports.
func TestHelperPickerWithoutAliases(t *testing.T) {
	up, _ := aliasUpstream(t)
	_, _, br, csrf, _ := signedInProvider(t, up)
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	_, page := br.get("/admin")
	if strings.Contains(page, "(alias of") || !strings.Contains(page, "3 models, loaded") {
		t.Error("no aliases means no alias entries")
	}
}
