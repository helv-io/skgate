package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/helv-io/skgate/internal/admin"
	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/oidctest"
)

// In a real browser (optional): no admin page has a horizontal scrollbar at 320, 360, 390, 768, 1024, 1280 and
// 1920 px, with long names, URLs, labels and tokens, also with the Details dialog open. SKGATE_SHOTS=<dir> also
// saves a screenshot of every page at 390 and 1280 px. Needs SKGATE_CHROME and SKGATE_PUPPETEER like the other browser checks.
func TestNoHorizontalScrollInBrowser(t *testing.T) {
	chrome, pp := os.Getenv("SKGATE_CHROME"), os.Getenv("SKGATE_PUPPETEER")
	node, err := exec.LookPath("node")
	if chrome == "" || pp == "" || err != nil {
		t.Skip("set SKGATE_CHROME and SKGATE_PUPPETEER (and install node) to run the browser layout check")
	}
	longName := strings.Repeat("segment-", 8) + "end"
	up, _ := aliasUpstream(t) // the long skgate alias below is listed in the picker, so its label is swept too
	a, _, br, csrf, _ := signedInProvider(t, up)
	gh, _ := fakeGitHub(t, 200, release("v99.0.0", false)) // a newer release, so the header link is swept in its yellow state
	a.Admin.Releases = admin.NewReleaseWatch(gh.URL, config.Version)
	a.Admin.Releases.Refresh(context.Background())
	post := func(path string, v url.Values) string {
		v.Set("csrf", csrf)
		_, body := br.post(path, v)
		return body
	}
	post("/admin/providers/grok/models/reload", url.Values{})
	post("/admin/providers/grok/aliases/put", url.Values{"name": {"grok-latest"}, "target": {"grok-4.7-reasoning"}})
	post("/admin/providers/grok/model", url.Values{"model": {strings.Repeat("a", 60)}, "effort": {"medium"}}) // the long alias as helper model, with its reasoning pill
	post("/admin/providers/grok/aliases/put", url.Values{"name": {strings.Repeat("a", 60)}, "target": {"grok-4.7-reasoning"}})
	post("/admin/providers/add", url.Values{"preset": {"openai"}, "base": {up.URL + "/v1"}, "key": {"sk-" + strings.Repeat("k", 60)}}) // key-based providers: cards, dialogs and the alias overview
	post("/admin/providers/openai/models/reload", url.Values{})
	post("/admin/providers/openai/aliases/put", url.Values{"name": {strings.Repeat("o", 60)}, "target": {"plain"}})
	post("/admin/providers/add", url.Values{"preset": {"custom"}, "base": {"https://llm.example.com/" + strings.Repeat("very/long/", 12) + "v1"}})
	post("/admin/upstreams/save", stdioForm(csrf, "mgd", url.Values{"lifecycle": {"always"}, "args": {"--config", "/very/long/path/" + strings.Repeat("dir/", 20) + "file.json"}}))
	post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {longName[:63]},
		"url": {"https://mcp.example.com/" + strings.Repeat("a/very/long/path/", 12) + "mcp?x=" + strings.Repeat("q", 80)}, "auth_kind": {"header"},
		"auth_name": {"X-Api-Key"}, "auth_value": {"k-" + strings.Repeat("v", 60)}, "hdr_name": {"X-A"}, "hdr_value": {"1"},
		"host_override": {strings.Repeat("h", 40) + ".internal.example.com:8000"}, "enabled": {"1"}, "include": {"1"}})
	post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"remote"}, "alias": {"docs"}, "url": {"http://127.0.0.1:1/mcp"}, "auth_kind": {"auto"}})
	post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"git"}, "alias": {"gitsrv"}, "git_url": {"https://git.example.com/" + strings.Repeat("org/", 15) + "repo.git"},
		"command": {"python3"}, "args": {"-m", "srv"}, "lifecycle": {"on-demand"}, "enabled": {"1"}})
	// an OpenAPI upstream with long names and more tools than a model handles, so the red counter, the callout and the
	// verb groups are swept too
	var oaPaths []string
	for i := 0; i < 36; i++ {
		long := fmt.Sprintf("/v1/%s/{id}/%d", strings.Repeat("a-very-long-path-segment/", 5), i)
		oaPaths = append(oaPaths, fmt.Sprintf(`%q:{"get":{"operationId":"get%s%d","summary":"Reads item %d with a long summary %s"},"delete":{"operationId":"delete%s%d","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]}}`,
			long, strings.Repeat("Item", 12), i, i, strings.Repeat("that goes on ", 8), strings.Repeat("Item", 12), i))
	}
	oaSpec := `{"openapi":"3.0.0","info":{"title":"` + strings.Repeat("Long API title ", 4) + `","version":"1"},"servers":[{"url":"https://api.example.com/` + strings.Repeat("base/", 14) + `"}],"paths":{` + strings.Join(oaPaths, ",") + `}}`
	post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {longName[:62] + "o"}, "oa_spec_text": {oaSpec},
		"oa_auth_kind": {"query"}, "oa_auth_name": {"api_key"}, "oa_auth_value": {"k-" + strings.Repeat("v", 60)}, "enabled": {"1"}, "include": {"1"}})
	// an OpenAPI upstream read from an address, then a changed description: the Update review screen with long names
	var specMu sync.Mutex
	specText := `{"openapi":"3.0.0","info":{"title":"U","version":"1"},"servers":[{"url":"http://x.example"}],"paths":{"/a":{"get":{"operationId":"getA"}}}}`
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		specMu.Lock()
		defer specMu.Unlock()
		io.WriteString(w, specText)
	}))
	t.Cleanup(specSrv.Close)
	post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"fromurl"}, "oa_spec_url": {specSrv.URL}, "enabled": {"1"}})
	var more []string
	for i := 0; i < 8; i++ {
		more = append(more, fmt.Sprintf(`"/v2/%s/{id}/%d":{"get":{"operationId":"added%d","summary":"New"}}`, strings.Repeat("a-very-long-path-segment/", 5), i, i))
	}
	specMu.Lock()
	specText = `{"openapi":"3.0.0","info":{"title":"U","version":"2"},"servers":[{"url":"http://x.example"}],"paths":{"/a":{"get":{"operationId":"getA","summary":"Changed"}},` + strings.Join(more, ",") + `}}`
	specMu.Unlock()
	_, updateReview := br.post("/admin/upstreams/fromurl/spec/update", url.Values{"csrf": {csrf}})
	var on, keys, names, descs []string
	for i := 0; i < 36; i++ {
		k := fmt.Sprintf("GET /v1/%s/{id}/%d", strings.Repeat("a-very-long-path-segment/", 5), i)
		on, keys, names, descs = append(on, k), append(keys, k), append(names, fmt.Sprintf("get%s%d", strings.Repeat("Item", 12), i)), append(descs, "")
	}
	post("/admin/upstreams/"+longName[:62]+"o/tools/save", url.Values{"on": on, "key": keys, "name": names, "desc": descs})
	for _, l := range []string{"production service key " + strings.Repeat("with a very long label ", 3), "short"} {
		post("/admin/keys/create", url.Values{"label": {l}, "rate": {"30"}, "expires": {"2031-12-31 18:00"}})
	}
	for _, n := range []string{"Home Assistant " + strings.Repeat("with a very long client name ", 3), "c2"} {
		post("/admin/clients/create", url.Values{"name": {n}, "redirects": {"https://my.home-assistant.io/redirect/" + strings.Repeat("oauth/", 15) + "callback\nhttps://x.example/cb"}, "method": {"client_secret_post"}})
	}
	if ks, _ := a.Keys.List(); len(ks) > 0 { // one key may be sent as ?key=: its row/card is tinted
		a.Keys.SetURLKey(ks[0].ID, true)
	}
	type page struct {
		Name   string `json:"name"`
		Dialog bool   `json:"dialog"`
	}
	var pages []page
	dir := t.TempDir()
	add := func(name, body string, dialog bool) {
		pages = append(pages, page{name, dialog})
		os.WriteFile(filepath.Join(dir, name+".html"), []byte(strings.ReplaceAll(body, "/admin/static/", "")), 0o600)
	}
	get := func(path string) string { _, b := br.get(path); return b }
	add("status", get("/admin"), true)
	add("keys", get("/admin/keys"), true)
	_, newKey := br.post("/admin/keys/create", url.Values{"csrf": {csrf}, "label": {"fresh"}})
	add("keys-new", newKey, false)
	add("upstreams", get("/admin/upstreams"), true)
	add("upstream-new", get("/admin/upstreams/new"), true)
	add("upstream-edit-remote", get("/admin/upstreams/"+longName[:63]+"/edit"), false)
	add("upstream-edit-managed", get("/admin/upstreams/mgd/edit"), false)
	add("upstream-edit-openapi", get("/admin/upstreams/"+longName[:62]+"o/edit"), false)
	add("upstream-update-review", updateReview, false)
	add("upstream-tools-url", get("/admin/upstreams/fromurl/tools"), false)
	add("upstream-tools", get("/admin/upstreams/"+longName[:62]+"o/tools"), false)
	add("upstream-test-openapi", get("/admin/upstreams/"+longName[:62]+"o/test"), false)
	add("upstream-test", get("/admin/upstreams/docs/test"), false)
	add("upstream-test-managed", get("/admin/upstreams/mgd/test"), false)
	add("upstream-logs", get("/admin/upstreams/mgd/logs"), false)
	add("upstream-import", post("/admin/upstreams/import", url.Values{"json": {`{"mcpServers": {"` + strings.Repeat("n", 40) + `": {"url": "https://h.example.com/` + strings.Repeat("p/", 40) + `"}, "bad": {}}}`}}), false)
	add("clients", get("/admin/clients"), true)
	_, newClient := br.post("/admin/clients/create", url.Values{"csrf": {csrf}, "name": {"n"}, "redirects": {"https://x.example/cb"}, "method": {"client_secret_post"}})
	add("clients-new", newClient, false)
	_ = a
	// standalone screens: consent (consent on, long client name and redirect), signed out, sign-in error, OIDC not configured
	_, ts2, br2, _ := signedIn(t, func(c *config.Config, _ *oidctest.Provider) { c.RequireConsent = true })
	redirect := "https://client.example.com/" + strings.Repeat("oauth/", 14) + "callback"
	reg, _ := json.Marshal(map[string]any{"client_name": "Home Assistant " + strings.Repeat("with a very long client name ", 2), "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"})
	resp, err := http.Post(ts2.URL+"/register", "application/json", strings.NewReader(string(reg)))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	clientID, _ := m["client_id"].(string)
	_, challenge := pkcePair()
	_, consent := br2.get(authorizePath(clientID, redirect, challenge))
	add("consent", consent, false)
	_, signedOut := br2.get("/admin/signed-out")
	add("signed-out", signedOut, false)
	_, authErr := br2.get("/admin/oidc/callback?error=access_denied&state=x")
	add("auth-error", authErr, false)
	_, notFound := br2.get("/admin/nothing-here")
	add("not-found", notFound, false)
	_, badClient := br2.get("/authorize?response_type=code&client_id=nope&redirect_uri=https://x.example/cb&code_challenge=abc&code_challenge_method=S256")
	add("authorize-error", badClient, false)
	_, ts3, _ := newApp(t, func(c *config.Config, _ *oidctest.Provider) { c.OIDCIssuer = "" })
	_, notConfigured := newBrowser(t, ts3).get("/admin")
	add("not-configured", notConfigured, false)
	b, _ := json.Marshal(pages)
	os.WriteFile(filepath.Join(dir, "pages.json"), b, 0o600)
	css, _ := os.ReadFile(filepath.Join("..", "admin", "static", "app.css"))
	js, _ := os.ReadFile(filepath.Join("..", "admin", "static", "app.js"))
	os.WriteFile(filepath.Join(dir, "app.css"), css, 0o600)
	os.WriteFile(filepath.Join(dir, "app.js"), js, 0o600)
	script, _ := filepath.Abs(filepath.Join("testdata", "sweep.js"))
	args := []string{script, dir, chrome, filepath.Join(pp, "node_modules", "puppeteer-core")}
	if shots := os.Getenv("SKGATE_SHOTS"); shots != "" {
		os.MkdirAll(shots, 0o755)
		args = append(args, shots)
	}
	out, err := exec.Command(node, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
