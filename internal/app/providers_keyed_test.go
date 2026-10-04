package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// openAIUpstream is an OpenAI-compatible server: it lists models and records the Authorization header and body of
// the last chat request.
func openAIUpstream(t *testing.T, models ...string) (*httptest.Server, func() (auth, body string)) {
	var mu sync.Mutex
	var auth, body string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			var parts []string
			for _, m := range models {
				parts = append(parts, `{"id":"`+m+`","object":"model"}`)
			}
			io.WriteString(w, `{"object":"list","data":[`+strings.Join(parts, ",")+`]}`)
			return
		}
		mu.Lock()
		auth, body = r.Header.Get("Authorization"), string(b)
		mu.Unlock()
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"from openai"}}]}`)
	}))
	t.Cleanup(ts.Close)
	return ts, func() (string, string) { mu.Lock(); defer mu.Unlock(); return auth, body }
}

func chat(t *testing.T, base, key, model string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func addProvider(br *browser, csrf, preset, base, key string) (*http.Response, string) {
	return br.post("/admin/providers/add", url.Values{"csrf": {csrf}, "preset": {preset}, "base": {base}, "key": {key}})
}

// Adding a provider saves its key sealed, tests the connection, and shows a card with the key masked.
func TestAddProviderSavesSealedKeyAndTests(t *testing.T) {
	grokUp, _ := modelsUpstream(t, "grok-4.7")
	oa, _ := openAIUpstream(t, "gpt-x", "gpt-y")
	a, _, br, csrf, _ := signedInProvider(t, grokUp)
	resp, _ := addProvider(br, csrf, "openai", oa.URL+"/v1", " sk-live-abcd1234 ")
	if k, m := flashOf(resp); k != "ok" || !strings.Contains(m, "connected, 2 models") {
		t.Fatalf("flash %q %q", k, m)
	}
	raw, _ := a.DB.GetSetting("provider.openai.key")
	if raw == "" || strings.Contains(raw, "abcd1234") {
		t.Fatalf("key not sealed: %q", raw)
	}
	_, page := br.get("/admin")
	for _, want := range []string{"<h3>OpenAI</h3>", "************1234", `data-dialog-open="#provider-openai"`, "Test connection", "Remove provider", `value="` + oa.URL + `/v1"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "sk-live") || strings.Contains(page, "abcd1234") {
		t.Fatal("the API key is on the page")
	}
	// the key field never carries the key, even masked, as its value
	if strings.Contains(page, `name="key" autocomplete="new-password" spellcheck="false" value=`) {
		t.Fatal("key input prefilled")
	}
}

func TestAddProviderValidationAndFailedTest(t *testing.T) {
	grokUp, _ := modelsUpstream(t, "grok-4.7")
	a, _, br, csrf, _ := signedInProvider(t, grokUp)
	for name, f := range map[string]struct{ preset, base, key, want string }{
		"unknown":   {"nope", "", "k", "choose a provider"},
		"no key":    {"openai", "", "", "enter the API key"},
		"no base":   {"custom", "", "", "enter the base URL"},
		"bad base":  {"custom", "ftp://x", "", "absolute http(s)"},
		"bad base2": {"ollama", "not a url", "", "absolute http(s)"},
	} {
		resp, _ := addProvider(br, csrf, f.preset, f.base, f.key)
		if k, m := flashOf(resp); k != "bad" || !strings.Contains(m, f.want) {
			t.Errorf("%s: flash %q %q", name, k, m)
		}
	}
	if v, _ := a.DB.GetSetting("provider.openai.enabled"); v != "" {
		t.Fatal("a refused add must save nothing")
	}
	// An unreachable server still saves the provider and says the test failed.
	resp, _ := addProvider(br, csrf, "ollama", "http://127.0.0.1:1/v1", "")
	if k, m := flashOf(resp); k != "bad" || !strings.Contains(m, "Ollama added, but the connection failed") {
		t.Fatalf("flash %q %q", k, m)
	}
	if v, _ := a.DB.GetSetting("provider.ollama.enabled"); v != "1" {
		t.Fatal("saved anyway")
	}
}

// An alias is a provider plus a model: a request for the alias reaches that provider with its key and its model.
func TestAliasAcrossProvidersRoutesRequests(t *testing.T) {
	grokUp, grokSent := aliasUpstream(t)
	oa, seen := openAIUpstream(t, "gpt-x", "gpt-y")
	a, ts, br, csrf, apiKey := signedInProvider(t, grokUp)
	addProvider(br, csrf, "openai", oa.URL+"/v1", "sk-live-abcd1234")
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	resp, _ := br.post("/admin/providers/openai/aliases/put", url.Values{"csrf": {csrf}, "name": {"smart"}, "target": {"gpt-y"}})
	if k, _ := flashOf(resp); k != "ok" {
		t.Fatal("alias refused")
	}
	if st, _ := chat(t, ts.URL, apiKey, "smart"); st != 200 {
		t.Fatalf("status %d", st)
	}
	auth, body := seen()
	if auth != "Bearer sk-live-abcd1234" || !strings.Contains(body, `"model":"gpt-y"`) {
		t.Fatalf("openai saw %q %q", auth, body)
	}
	if len(grokSent()) != 0 {
		t.Fatal("Grok must not see it")
	}
	// A model only OpenAI lists goes there too; a Grok model stays with Grok.
	chat(t, ts.URL, apiKey, "gpt-x")
	if _, b := seen(); !strings.Contains(b, `"model":"gpt-x"`) {
		t.Fatalf("body %q", b)
	}
	chat(t, ts.URL, apiKey, "plain")
	if got := grokSent(); len(got) != 1 || got[0] != "plain" {
		t.Fatalf("grok saw %v", got)
	}
	// The model list carries the alias.
	req, _ := http.NewRequest("GET", ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	r, _ := http.DefaultClient.Do(req)
	lb, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(lb), `"smart"`) {
		t.Fatalf("list: %s", lb)
	}
	// The same alias name under another provider moves it.
	resp, _ = br.post("/admin/providers/grok/aliases/put", url.Values{"csrf": {csrf}, "name": {"smart"}, "target": {"plain"}})
	if _, m := flashOf(resp); !strings.Contains(m, "moved here from OpenAI") {
		t.Fatalf("flash %q", m)
	}
	if len(a.Admin.Set.Aliases("openai")) != 0 || len(a.Admin.Set.Aliases("grok")) != 1 {
		t.Fatal("alias not moved")
	}
}

// The MCP helper can use a model of another provider, also when Grok is signed out.
func TestHelperModelOfAnotherProvider(t *testing.T) {
	oa, seen := openAIUpstream(t, "gpt-x")
	a, _, br, csrf := signedIn(t, nil)
	_, page := br.get("/admin/upstreams/new")
	if !strings.Contains(page, `disabled title="sign in on the status page"`) {
		t.Fatal("Suggest must be off with nothing ready")
	}
	addProvider(br, csrf, "openai", oa.URL+"/v1", "sk-live-abcd1234")
	_, page = br.get("/admin")
	if !strings.Contains(page, `<optgroup label="OpenAI models">`) || !strings.Contains(page, `<option value="gpt-x"`) {
		t.Fatalf("picker lacks the OpenAI models")
	}
	resp, _ := br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"gpt-x"}})
	if k, m := flashOf(resp); k != "ok" {
		t.Fatalf("flash %q %q", k, m)
	}
	resp, _ = br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"unknown-model"}})
	if k, _ := flashOf(resp); k != "bad" {
		t.Fatal("an unknown model must be refused")
	}
	_, page = br.get("/admin/upstreams/new")
	if strings.Contains(page, `disabled title="sign in on the status page"`) || !strings.Contains(page, "MCP helper model: gpt-x") {
		t.Fatal("Suggest should be available")
	}
	st, _, err := a.Proxy.Post(t.Context(), "/chat/completions", []byte(`{"model":"gpt-x","messages":[]}`))
	if err != nil || st != 200 {
		t.Fatalf("%d %v", st, err)
	}
	if auth, _ := seen(); auth != "Bearer sk-live-abcd1234" {
		t.Fatalf("auth %q", auth)
	}
}

// Test connection, key replacement (empty keeps the key), local servers without a key, removal.
func TestKeyedDialogActionsAndRemoval(t *testing.T) {
	grokUp, _ := modelsUpstream(t, "grok-4.7")
	oa, seen := openAIUpstream(t, "gpt-x")
	a, ts, br, csrf, apiKey := signedInProvider(t, grokUp)
	addProvider(br, csrf, "openai", oa.URL+"/v1", "sk-first-1111")
	resp, _ := br.post("/admin/providers/openai/test", url.Values{"csrf": {csrf}})
	if k, m := flashOf(resp); k != "ok" || !strings.Contains(m, "connected, 1 models") {
		t.Fatalf("%q %q", k, m)
	}
	br.post("/admin/providers/openai/key", url.Values{"csrf": {csrf}, "base": {oa.URL + "/v1"}, "key": {""}})
	chat(t, ts.URL, apiKey, "gpt-x")
	if auth, _ := seen(); auth != "Bearer sk-first-1111" {
		t.Fatalf("empty key must keep it: %q", auth)
	}
	br.post("/admin/providers/openai/key", url.Values{"csrf": {csrf}, "base": {oa.URL + "/v1"}, "key": {"sk-second-2222"}})
	chat(t, ts.URL, apiKey, "gpt-x")
	if auth, _ := seen(); auth != "Bearer sk-second-2222" {
		t.Fatalf("new key: %q", auth)
	}
	// Grok-only routes refuse key-based providers.
	if resp, _ := br.post("/admin/providers/openai/key", url.Values{"csrf": {csrf}}); resp.StatusCode == 404 {
		t.Fatal("key route missing")
	}
	if resp, _ := br.post("/admin/providers/grok/key", url.Values{"csrf": {csrf}}); resp.StatusCode != 404 {
		t.Fatalf("Grok has no API key: %d", resp.StatusCode)
	}
	br.post("/admin/providers/openai/models/reload", url.Values{"csrf": {csrf}})
	br.post("/admin/providers/openai/aliases/put", url.Values{"csrf": {csrf}, "name": {"smart"}, "target": {"gpt-x"}})
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"smart"}})
	resp, _ = br.post("/admin/providers/openai/remove", url.Values{"csrf": {csrf}})
	if k, m := flashOf(resp); k != "ok" || !strings.Contains(m, "OpenAI removed") {
		t.Fatalf("%q %q", k, m)
	}
	for _, k := range []string{"key", "enabled", "aliases"} {
		if v, ok := a.DB.GetSetting("provider.openai." + k); ok || v != "" {
			t.Errorf("provider.openai.%s survived removal", k)
		}
	}
	if a.Admin.Set.Model("grok") != "" {
		t.Fatal("the helper model pointed into the removed provider and must be cleared")
	}
	_, page := br.get("/admin")
	if strings.Contains(page, "<h3>OpenAI</h3>") {
		t.Fatal("card still shown")
	}
}

// A local server needs no key and gets no Authorization header.
func TestLocalProviderSendsNoAuthorization(t *testing.T) {
	grokUp, _ := modelsUpstream(t, "grok-4.7")
	local, seen := openAIUpstream(t, "llama3")
	_, ts, br, csrf, apiKey := signedInProvider(t, grokUp)
	resp, _ := addProvider(br, csrf, "ollama", local.URL+"/v1", "")
	if k, m := flashOf(resp); k != "ok" {
		t.Fatalf("%q %q", k, m)
	}
	chat(t, ts.URL, apiKey, "llama3")
	if auth, body := seen(); auth != "" || !strings.Contains(body, "llama3") {
		t.Fatalf("auth %q body %q", auth, body)
	}
	_, page := br.get("/admin")
	d := page[strings.Index(page, `id="provider-ollama"`):]
	d = d[:strings.Index(d, "</template>")]
	if strings.Contains(d, `name="key"`) {
		t.Fatal("the Ollama dialog asks for a key")
	}
}

// The Add dialog lists every preset with its base URL and hint as data.
func TestAddDialogListsPresets(t *testing.T) {
	_, _, br, _ := signedIn(t, nil)
	_, page := br.get("/admin")
	d := page[strings.Index(page, `id="add-provider"`):]
	d = d[:strings.Index(d, "</template>")]
	for _, want := range []string{"OpenAI", "Anthropic", "Google Gemini", "Mistral", "DeepSeek", "Groq", "OpenRouter", "Ollama", "LM Studio", "Custom endpoint",
		`data-set-base="https://api.anthropic.com/v1"`, `data-set-base="http://localhost:11434/v1"`, `data-preset`, `data-preset-href="docs"`} {
		if !strings.Contains(d, want) {
			t.Errorf("Add dialog lacks %q", want)
		}
	}
}

// Warming the model lists at start never fails or blocks it: an unreachable provider is skipped.
func TestWarmModelsIgnoresUnreachableProviders(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	addProvider(br, csrf, "ollama", "http://127.0.0.1:1/v1", "")
	done := make(chan struct{})
	go func() { a.WarmModels(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("WarmModels blocked")
	}
}

// onlyV1Upstream serves a model list at /v1/models only, for the key "sk-good"; the paths and keys it does not
// know get 404 and 401.
func onlyV1Upstream(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-good" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"m1"},{"id":"m2"},{"id":"m3"}]}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// Whatever form of the address is typed, the one that works is saved, and nothing about the forms is said.
func TestAddProviderFindsTheWorkingAddressForm(t *testing.T) {
	for _, typed := range []string{"", "/", "/v1", "/v1/", "/v1/chat/completions"} {
		t.Run("typed"+typed, func(t *testing.T) {
			grokUp, _ := modelsUpstream(t, "grok-4.7")
			up := onlyV1Upstream(t)
			_, _, br, csrf, _ := signedInProvider(t, grokUp)
			resp, _ := addProvider(br, csrf, "custom", up.URL+typed, "sk-good")
			k, m := flashOf(resp)
			if k != "ok" || !strings.Contains(m, "connected, 3 models") || strings.Contains(m, "/v1") {
				t.Fatalf("flash %q %q", k, m)
			}
			_, page := br.get("/admin")
			if !strings.Contains(page, `value="`+up.URL+`/v1"`) {
				t.Errorf("the working form was not saved")
			}
		})
	}
}

// When no form works there is one plain sentence, the provider stays saved, and the address stays as typed.
func TestAddProviderPlainErrors(t *testing.T) {
	grokUp, _ := modelsUpstream(t, "grok-4.7")
	up := onlyV1Upstream(t)
	_, _, br, csrf, _ := signedInProvider(t, grokUp)
	resp, _ := addProvider(br, csrf, "custom", up.URL, "sk-wrong")
	if k, m := flashOf(resp); k != "bad" || !strings.Contains(m, "did not accept the API key") || strings.Contains(m, "/v1") {
		t.Errorf("wrong key: %q %q", k, m)
	}
	br.post("/admin/providers/custom/remove", url.Values{"csrf": {csrf}})
	nothing := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(nothing.Close)
	resp, _ = addProvider(br, csrf, "custom", nothing.URL+"/x", "sk-good")
	if k, m := flashOf(resp); k != "bad" || !strings.Contains(m, "no model list was found at that address") {
		t.Errorf("nothing found: %q %q", k, m)
	}
	_, page := br.get("/admin")
	if !strings.Contains(page, `value="`+nothing.URL+`/x"`) {
		t.Errorf("the address was not kept as typed")
	}
}
