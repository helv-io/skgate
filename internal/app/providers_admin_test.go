package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"rsc.io/qr"

	"github.com/helv-io/skgate/internal/mcp"
	"github.com/helv-io/skgate/internal/provider/grok"
	"github.com/helv-io/skgate/internal/timefmt"
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
	for _, want := range []string{`<span class="pill ok"`, "<h3>Grok</h3>", `data-dialog-open="#provider-grok"`, ">Details</button>"} {
		if !strings.Contains(main, want) {
			t.Errorf("main screen lacks %q", want)
		}
	}
	if strings.Contains(main, "Sign in again") || strings.Contains(main, ">Sign in</button>") || strings.Contains(main, "Sign out") {
		t.Error("a signed-in card does not offer sign-in or sign-out")
	}
	for _, gone := range []string{"UPSTREAM_BASE", "Upstream base", "PKCE", "Scopes", "Client ID", "skgate 0"} {
		if strings.Contains(main, gone) {
			t.Errorf("main screen still has %q", gone)
		}
	}
	// Details is the helper model, the aliases and Remove. Tokens are not shown.
	for _, want := range []string{"MCP helper model", "Model aliases", "Remove provider", `data-confirm="Its aliases go too."`} {
		if !strings.Contains(dlg, want) {
			t.Errorf("dialog lacks %q", want)
		}
	}
	for _, gone := range []string{"Refresh token", "************5678", "************1234", "PKCE", "Token endpoint", "Technical details", "Refresh now"} {
		if strings.Contains(dlg, gone) {
			t.Errorf("dialog still has %q", gone)
		}
	}
	card := grokCard(page)
	if !regexp.MustCompile(`<th>Expires</th><td>\d{4}-\d{2}-\d{2} \d{2}:\d{2}</td>`).MatchString(card) {
		t.Fatalf("signed-in card must show Expires as a timestamp:\n%s", card)
	}
	if strings.Contains(strings.ToLower(card), "token in") || strings.Contains(card, "<th>Token</th>") {
		t.Fatalf("the card must not say Token in:\n%s", card)
	}
	prov := page[strings.Index(page, `id="provider-grok"`):]
	prov = prov[:strings.Index(prov, "</template>")]
	for _, gone := range []string{"Upstream URLs", "Fallback", `name="base"`, `name="fallback"`, `data-section="upstream"`, "Technical details"} {
		if strings.Contains(prov, gone) {
			t.Errorf("Grok details still has %q", gone)
		}
	}
	if !strings.Contains(prov, "New alias") || !strings.Contains(prov, "Remove provider") {
		t.Error("Grok details lacks the alias list or Remove")
	}
	if !strings.Contains(page, ">Base URL<") {
		t.Error("adding a provider still asks for a base URL")
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
	if !regexp.MustCompile(`<button type="button" class="pill warn" data-dialog-open="#aliases-grok" title="[^"]*">1 of 1 alias stale</button>`).MatchString(page) {
		t.Fatal("summary on the main screen must warn too")
	}
	// delete
	post("/admin/providers/grok/aliases/delete", url.Values{"name": {"grok"}})
	api("POST", "/v1/chat/completions", `{"model":"grok"}`)
	if !strings.Contains(*last, `"model":"grok"`) {
		t.Fatalf("deleted alias still rewritten: %s", *last)
	}
}

// The MCP helper model can only be one of the provider's models, and is kept per provider.
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
	if !strings.Contains(page, `<button type="button" class="pill ok" data-dialog-open="#helper-model" title="used as the MCP helper model">m2 · reasoning auto</button>`) {
		t.Fatal("model summary missing on the main screen")
	}
	if resp, _ := br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"m2"}}); resp.Header.Get("Location") != "/admin#helper-model" {
		t.Fatalf("a saved helper model returns to its dialog: %q", resp.Header.Get("Location"))
	}
	post(url.Values{"model": {""}})
	_, page = br.get("/admin")
	if !strings.Contains(page, `data-dialog-open="#helper-model"`) || !strings.Contains(page, ">no model</button>") {
		t.Fatal("cleared model must show the off pill, and it still opens the picker")
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

// The status pill is a plain status. Signed in, the card is Details. Remove, with its confirmation, is in Details.
// Sign in is primary only when the session is dead.
func TestSignOutIsAButtonAndThePillIsPlain(t *testing.T) {
	up, _ := modelsUpstream(t, "real-a")
	_, _, br, csrf, _ := signedInProvider(t, up)
	_, page := br.get("/admin")
	card := grokCard(page)
	actions := cardActions(card)
	for _, want := range []string{`<span class="pill ok"`, `data-dialog-open="#provider-grok">Details</button>`} {
		if !strings.Contains(card, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	if strings.Contains(actions, "Sign in") || strings.Contains(actions, "device/start") || strings.Contains(actions, "Sign out") || strings.Contains(actions, "Remove") {
		t.Errorf("signed in card offers more than Details:\n%s", actions)
	}
	dlg := page[strings.Index(page, `id="provider-grok"`):]
	dlg = dlg[:strings.Index(dlg, "</template>")]
	if !strings.Contains(dlg, `action="/admin/providers/grok/remove"`) || !strings.Contains(dlg, `data-confirm="Its aliases go too."`) || !strings.Contains(dlg, `data-confirm-ok="Remove"`) || !strings.Contains(dlg, `class="act danger">Remove provider</button>`) {
		t.Error("Details does not offer Remove with its confirmation")
	}
	if !strings.Contains(card, `<span class="pill ok"`) {
		t.Error("the status pill is a plain span")
	}
	if !strings.Contains(card, `data-dialog-open="#helper-model"`) || !strings.Contains(card, `data-dialog-open="#aliases-grok"`) {
		t.Error("the helper and alias pills open their dialogs")
	}
	if strings.Contains(card, "swap") || strings.Contains(card, `class="btn"`) {
		t.Errorf("nothing is primary while healthy:\n%s", card)
	}
	css := appCSS(t)
	if strings.Contains(css, ".pill.swap") {
		t.Error("the swap pill is gone from the stylesheet")
	}
	if !strings.Contains(css, "button.pill") {
		t.Error("a pill that opens a dialog is styled as a pill")
	}
	br.post("/admin/providers/grok/signout", url.Values{"csrf": {csrf}})
	_, page = br.get("/admin")
	card = grokCard(page)
	actions = cardActions(card)
	if !strings.Contains(card, `<span class="pill bad"`) || !strings.Contains(actions, `<button class="btn">Sign in</button>`) || strings.Contains(actions, "signout") || strings.Contains(actions, "Sign in again") || strings.Contains(actions, "Details") {
		t.Errorf("signed out: a plain pill and Sign in, in place of the signed-in state:\n%s", actions)
	}
}

// Sign-out drops the aliases of that provider and leaves the rest: another provider's alias, an upstream,
// and the helper model.
func TestSignOutClearsOnlyThatProvidersAliases(t *testing.T) {
	up, _ := modelsUpstream(t, "grok-4.7", "grok-mini")
	oa, _ := openAIUpstream(t, "gpt-x")
	remote := fakeUpstream(t)
	a, _, br, csrf, _ := signedInProvider(t, up)
	if err := a.MCP.Upstreams.Create(mcp.Upstream{Alias: "keep", URL: remote.URL, AuthKind: mcp.AuthNone, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	post := func(path string, v url.Values) *http.Response {
		v.Set("csrf", csrf)
		r, _ := br.post(path, v)
		return r
	}
	if k, m := flashOf(post("/admin/providers/grok/models/reload", url.Values{})); k != "ok" {
		t.Fatalf("reload: %s %s", k, m)
	}
	if k, m := flashOf(post("/admin/providers/grok/aliases/put", url.Values{"name": {"fast"}, "target": {"grok-mini"}})); k != "ok" {
		t.Fatalf("grok alias: %s %s", k, m)
	}
	if k, m := flashOf(post("/admin/providers/grok/model", url.Values{"model": {"grok-mini"}})); k != "ok" {
		t.Fatalf("helper: %s %s", k, m)
	}
	if resp, _ := addProvider(br, csrf, "openai", oa.URL+"/v1", "sk-live-abcd1234"); flashKind(resp) != "ok" {
		k, m := flashOf(resp)
		t.Fatalf("openai: %s %s", k, m)
	}
	if k, m := flashOf(post("/admin/providers/openai/aliases/put", url.Values{"name": {"smart"}, "target": {"gpt-x"}})); k != "ok" {
		t.Fatalf("openai alias: %s %s", k, m)
	}
	if k, m := flashOf(post("/admin/providers/grok/signout", url.Values{})); k != "ok" {
		t.Fatalf("sign out: %s %s", k, m)
	}
	if n := len(a.Admin.Set.Aliases("grok")); n != 0 {
		t.Fatalf("grok aliases left: %d", n)
	}
	if als := a.Admin.Set.Aliases("openai"); len(als) != 1 || als[0].Name != "smart" || als[0].Target != "openai_gpt-x" {
		t.Fatalf("openai aliases: %+v", als)
	}
	if _, ok := a.MCP.Upstreams.Get("keep"); !ok {
		t.Fatal("sign-out removed an upstream")
	}
	if a.Admin.Set.Model("grok") != "grok-mini" {
		t.Fatal("sign-out cleared the helper model")
	}
	_, page := br.get("/admin")
	card := grokCard(page)
	if !strings.Contains(card, `>Sign in</button>`) || strings.Contains(card, ">no aliases</button>") || strings.Contains(card, "fast") {
		t.Fatalf("a signed-out card is Sign in, not the alias list:\n%s", card)
	}
	if !strings.Contains(page, `data-dialog-open="#aliases-openai"`) || !strings.Contains(page, ">1 alias</button>") {
		t.Fatal("the other provider's alias is still on its card")
	}
	if strings.Contains(card, "<th>Expires</th>") || strings.Contains(strings.ToLower(card), "token in") {
		t.Fatalf("a signed-out card has no expiry row:\n%s", card)
	}
}

// The signed-in card says when the token expires, as a date and time, and never "Token in".
func TestProviderCardShowsExpiryAsATimestamp(t *testing.T) {
	up, _ := modelsUpstream(t, "m")
	a, _, br, _, _ := signedInProvider(t, up)
	when := time.Now().Add(6 * time.Hour)
	a.Providers.Default().(*grok.Client).SetTokens("acc-secret-1234", "refresh-secret-5678", when)
	_, page := br.get("/admin")
	card := grokCard(page)
	want := timefmt.DateTime(time.Unix(when.Unix(), 0))
	if !strings.Contains(card, "<th>Expires</th><td>"+want+"</td>") {
		t.Fatalf("expiry row want %q:\n%s", want, card)
	}
	if strings.Contains(strings.ToLower(page), "token in") || strings.Contains(card, "<th>Token</th>") {
		t.Fatal("the page still says Token in")
	}
}

// Device sign-in shows the code and the plain verification address as link text. The popup,
// the link href and the QR open the address with the user code: the provider's complete URI
// when it sent one, otherwise the address with the code appended.
func TestDevicePanelShowsTheCodeTheAddressAndAQR(t *testing.T) {
	const (
		verify = "https://accounts.x.ai/oauth2/device"
		code   = "ABCD-1234"
	)
	open := verify + "?user_code=" + code
	// A provider's own complete URI is not rebuilt, even when it is not user_code on verification_uri.
	other := "https://login.example/device?otc=" + code
	for _, tc := range []struct {
		name, complete, open string
	}{
		{"complete URI", open, open},
		{"provider form", other, other},
		{"built from the code", "", open},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := devicePanelCard(t, verify, code, tc.complete)
			link := `<a href="` + tc.open + `" target="_blank" rel="noopener noreferrer">` + verify + `</a>`
			for _, want := range []string{
				`data-device-url="` + tc.open + `"`,
				"Open the window and enter this code.",
				"If the window didn't open, " + link,
				`class="copybox big" data-copy-text="` + code + `"`,
				"Tap to copy",
				">Cancel</button>",
			} {
				if !strings.Contains(card, want) {
					t.Errorf("device panel lacks %q", want)
				}
			}
			if strings.Contains(card, ">"+tc.open+"<") {
				t.Fatal("the link text includes the user code")
			}
			if !strings.Contains(card, signInQR(tc.open)) {
				t.Error("the QR does not encode the address with the code")
			}
			if plain := signInQR(verify); plain != signInQR(tc.open) && strings.Contains(card, plain) {
				t.Fatal("the QR encodes the address without the code")
			}
		})
	}
	js, err := os.ReadFile("../admin/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `window.open(open, "skgate-device", "width=520,height=720,noopener,noreferrer")`) {
		t.Fatal("the panel does not open the verification address in a window")
	}
}

// devicePanelCard starts a device sign-in against a fake issuer and returns the Grok card.
// complete is the provider's verification_uri_complete; empty means the provider omitted it.
func devicePanelCard(t *testing.T, verify, code, complete string) string {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": srv.URL + "/oauth2/authorize", "device_authorization_endpoint": srv.URL + "/oauth2/device/code",
			"token_endpoint": srv.URL + "/oauth2/token", "userinfo_endpoint": srv.URL + "/oauth2/userinfo"})
	})
	mux.HandleFunc("/oauth2/device/code", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostFormValue("client_id") == "" {
			w.WriteHeader(400)
			return
		}
		body := map[string]any{
			"device_code": "DEV", "user_code": code, "verification_uri": verify,
			"expires_in": 600, "interval": 3600,
		}
		if complete != "" {
			body["verification_uri_complete"] = complete
		}
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	up, _ := modelsUpstream(t, "m")
	a, _, br, csrf, _ := signedInProvider(t, up)
	g := a.Providers.Default().(*grok.Client)
	t.Cleanup(g.CancelDevice)
	g.SignOut()
	g.Issuer = srv.URL
	resp, _ := br.post("/admin/providers/grok/device/start", url.Values{"csrf": {csrf}})
	if k, m := flashOf(resp); resp.StatusCode != http.StatusSeeOther || k == "bad" {
		t.Fatalf("start: %d %s %s", resp.StatusCode, k, m)
	}
	_, page := br.get("/admin")
	return grokCard(page)
}

// signInQR is the SVG the device panel draws for text. It matches admin.qrSVG.
func signInQR(text string) string {
	code, err := qr.Encode(text, qr.M)
	if err != nil || code.Size < 1 {
		return ""
	}
	const quiet = 4
	n := code.Size + quiet*2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="qr" viewBox="0 0 %d %d" role="img" aria-label="Sign-in QR">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/>`, n, n)
	b.WriteString(`<path fill="#111" d="`)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

func grokCard(page string) string {
	card := page[strings.Index(page, `id="grok"`):]
	return card[:strings.Index(card, "<h2>Other providers</h2>")]
}

func cardActions(card string) string {
	actions := card[strings.Index(card, `class="actions"`):]
	return actions[:strings.Index(actions, "</div>")]
}
