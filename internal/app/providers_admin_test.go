package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/provider/grok"
)

// modelsUpstream serves /v1/models and echoes the model of chat requests.
func modelsUpstream(t *testing.T, models ...string) (*httptest.Server, *string) {
	var lastBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			var data []map[string]string
			for _, m := range models {
				data = append(data, map[string]string{"id": m, "object": "model"})
			}
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(ts.Close)
	return ts, &lastBody
}

// signedInProvider returns an admin session with Grok signed in against up, and a virtual API key.
func signedInProvider(t *testing.T, up *httptest.Server) (*App, *httptest.Server, *browser, string, string) {
	a, ts, br, csrf := signedIn(t, nil)
	_ = a.DB.SetSetting("provider.grok.base", up.URL+"/v1") // the UI override wins over the built-in host
	_ = a.DB.SetSetting("provider.grok.fallback", "")
	a.Providers.Default().(*grok.Client).SetTokens("acc-secret-1234", "refresh-secret-5678", time.Now().Add(time.Hour))
	full, _, _ := a.Keys.Create("api")
	return a, ts, br, csrf, full
}

// The main screen shows status only: pills carry their detail in the hover text, the refresh token is
// never on the page, only in the details dialog.
func TestStatusPageAtAGlance(t *testing.T) {
	up, _ := modelsUpstream(t, "real-a")
	a, _, br, _, _ := signedInProvider(t, up)
	_, page := br.get("/admin")
	dlg := page[strings.Index(page, `<template data-dialog-content`):]
	main := page[:strings.Index(page, `<template data-dialog-content`)]
	if strings.Contains(main, "(ok)") || strings.Contains(main, "refresh-secret") || strings.Contains(main, "5678") || strings.Contains(main, "Refresh token") {
		t.Fatalf("main screen shows technical detail:\n%s", main)
	}
	for _, want := range []string{`<span class="pill ok">signed in</span>`, "<h3>Grok</h3>", `data-dialog-open="#provider-grok"`, "Sign in", "Sign out"} {
		if !strings.Contains(main, want) {
			t.Errorf("main screen lacks %q", want)
		}
	}
	for _, gone := range []string{"UPSTREAM_BASE", "Upstream base", "PKCE", "Scopes", "Client ID", "skgate 0"} {
		if strings.Contains(main, gone) {
			t.Errorf("main screen still has %q", gone)
		}
	}
	// the dialog holds the technical facts, masked
	for _, want := range []string{"Refresh token", "************5678", "************1234", "PKCE", "S256", "Token endpoint", "Base URL", "Fallback", "Helper model", "Model aliases"} {
		if !strings.Contains(dlg, want) {
			t.Errorf("dialog lacks %q", want)
		}
	}
	if strings.Contains(page, "refresh-secret-5678") || strings.Contains(page, "acc-secret-1234") {
		t.Fatal("a token is on the page in clear")
	}
	if strings.Count(page, "<dialog") != 1 {
		t.Fatal("exactly one shared modal per page")
	}
	// signed out: red pill with the reason in the hover text, nothing beside it
	a.Providers.Default().SignOut()
	_, page = br.get("/admin")
	if !strings.Contains(page, `<span class="pill bad">not signed in</span>`) || strings.Contains(page, "(") && strings.Contains(page, "not signed in</span> (") {
		t.Fatal("signed-out pill wrong")
	}
	// a failure is a tooltip on the pill
	a.DB.SetSetting("provider.grok.last_error", "refresh HTTP 500")
	_, page = br.get("/admin")
	if !regexp.MustCompile(`<span class="pill bad" title="[^"]*refresh HTTP 500[^"]*">not signed in</span>`).MatchString(page) {
		t.Fatal("last error must be the pill's hover text")
	}
}

// Aliases end to end through the app: the list, the rewrite, on every spelling, and the stale warning.
func TestProviderAliasesEndToEnd(t *testing.T) {
	up, last := modelsUpstream(t, "grok-4.7", "grok-mini")
	a, ts, br, csrf, key := signedInProvider(t, up)
	api := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	post := func(path string, v url.Values) *http.Response {
		v.Set("csrf", csrf)
		r, _ := br.post(path, v)
		return r
	}
	// reload the models, then create the alias in the dialog
	if k, m := flashOf(post("/admin/providers/grok/models/reload", url.Values{})); k != "ok" || !strings.Contains(m, "2 models") {
		t.Fatalf("reload: %s %s", k, m)
	}
	if k, m := flashOf(post("/admin/providers/grok/aliases/put", url.Values{"name": {"grok"}, "target": {"grok-4.7"}})); k != "ok" {
		t.Fatalf("put: %s %s", k, m)
	}
	// collision with a real id and an unknown target are refused
	for _, v := range []url.Values{{"name": {"grok-mini"}, "target": {"grok-4.7"}}, {"name": {"x"}, "target": {"nope"}}, {"name": {"a b"}, "target": {"grok-4.7"}}} {
		if k, _ := flashOf(post("/admin/providers/grok/aliases/put", v)); k != "bad" {
			t.Errorf("%v accepted", v)
		}
	}
	for _, pre := range []string{"", "/v1", "/api", "/api/v1"} {
		_, body := api("GET", pre+"/models", "")
		var l struct{ Data []struct{ ID string } }
		json.Unmarshal([]byte(body), &l)
		if len(l.Data) != 3 || l.Data[0].ID != "grok" || l.Data[1].ID != "grok-4.7" {
			t.Fatalf("%q list: %s", pre, body)
		}
		if st, _ := api("POST", pre+"/chat/completions", `{"model":"grok","messages":[]}`); st != 200 || !strings.Contains(*last, `"model":"grok-4.7"`) {
			t.Fatalf("%q rewrite: %d %s", pre, st, *last)
		}
	}
	// remapping applies at once
	post("/admin/providers/grok/aliases/put", url.Values{"name": {"grok"}, "target": {"grok-mini"}})
	api("POST", "/v1/responses", `{"model":"grok"}`)
	if !strings.Contains(*last, `"model":"grok-mini"`) {
		t.Fatalf("remap: %s", *last)
	}
	// healthy alias: an ok pill, no warning
	_, page := br.get("/admin")
	if strings.Contains(page, `pill warn`) {
		t.Fatal("no warning expected while the target is listed")
	}
	// the provider drops the target: warning pill with the detail as hover text
	a.Proxy.Models.Set("grok", []string{"grok-5"})
	_, page = br.get("/admin")
	if !regexp.MustCompile(`<span class="pill warn" title="target grok-mini is no longer in the provider&#39;s model list">stale</span>`).MatchString(page) {
		t.Fatalf("stale alias not flagged:\n%s", page[strings.Index(page, "Model aliases"):])
	}
	if !regexp.MustCompile(`<span class="pill warn" title="[^"]*">1 of 1 stale</span>`).MatchString(page) {
		t.Fatal("summary on the main screen must warn too")
	}
	// delete
	post("/admin/providers/grok/aliases/delete", url.Values{"name": {"grok"}})
	api("POST", "/v1/chat/completions", `{"model":"grok"}`)
	if !strings.Contains(*last, `"model":"grok"`) {
		t.Fatalf("deleted alias still rewritten: %s", *last)
	}
}

// The helper model can only be one of the provider's models, and is kept per provider.
func TestHelperModelSelection(t *testing.T) {
	up, _ := modelsUpstream(t, "m1", "m2")
	a, _, br, csrf, _ := signedInProvider(t, up)
	post := func(v url.Values) *http.Response {
		v.Set("csrf", csrf)
		r, _ := br.post("/admin/providers/grok/model", v)
		return r
	}
	if k, _ := flashOf(post(url.Values{"model": {"other"}})); k != "bad" {
		t.Fatal("unlisted model accepted")
	}
	if k, _ := flashOf(post(url.Values{"model": {"m2"}})); k != "ok" {
		t.Fatal("listed model refused")
	}
	if v, _ := a.DB.GetSetting("provider.grok.model"); v != "m2" {
		t.Fatalf("stored %q", v)
	}
	_, page := br.get("/admin")
	if !strings.Contains(page, `<span class="pill ok" title="used by the configuration helper">m2</span>`) {
		t.Fatal("model summary missing on the main screen")
	}
	post(url.Values{"model": {""}})
	_, page = br.get("/admin")
	if !strings.Contains(page, `class="pill off"`) || !strings.Contains(page, "no model") {
		t.Fatal("cleared model must show the off pill")
	}
}

// The proxy paths never shadow the app's own routes, and the old admin routes are gone.
func TestProxyPathsAndOtherRoutes(t *testing.T) {
	up, _ := modelsUpstream(t, "m")
	_, ts, _, _, key := signedInProvider(t, up)
	get := func(path string) int {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	for _, p := range []string{"/models", "/v1/models", "/api/models", "/api/v1/models"} {
		if get(p) != 200 {
			t.Errorf("%s not served", p)
		}
	}
	if get("/healthz") != 200 || get("/.well-known/oauth-authorization-server") != 200 {
		t.Error("own routes shadowed")
	}
	for _, p := range []string{"/admin/xai/refresh", "/admin/settings"} {
		if get(p) != 404 {
			t.Errorf("%s should be gone", p)
		}
	}
}
