package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/mcp"
)

const oaAdminSpec = `{"openapi":"3.0.0","info":{"title":"Notes API","version":"1"},"servers":[{"url":"%s/v1"}],
"paths":{"/notes":{"get":{"operationId":"listNotes","summary":"List notes"},"post":{"operationId":"addNote","summary":"Add a note","requestBody":{"content":{"application/json":{"schema":{"type":"object"}}}}}},
 "/notes/{id}":{"get":{"operationId":"getNote","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]},"delete":{"operationId":"deleteNote","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}]}},
 "/files":{"post":{"operationId":"upload","requestBody":{"content":{"multipart/form-data":{"schema":{"type":"object"}}}}}}}}`

// oaAPI is the REST API behind an OpenAPI upstream and the description it serves at /spec.json.
func oaAPI(t *testing.T) *httptest.Server {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/spec.json" {
			fmt.Fprintf(w, oaAdminSpec, ts.URL)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"seen":"%s %s","auth":"%s"}`, r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestOpenAPIAddByURLAndPickTools(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	api := oaAPI(t)
	r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"},
		"oa_spec_url": {api.URL + "/spec.json"}, "oa_auth_as": {"bearer"}, "oa_auth_value": {"SECRETTOKEN123"}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/upstreams/notes/tools" {
		t.Fatalf("add: %d %s %v", r.StatusCode, r.Header.Get("Location"), r.Header)
	}
	u, ok := a.MCP.Upstreams.Get("notes")
	if !ok || u.Kind != mcp.KindOpenAPI || u.URL != api.URL+"/v1" || u.AuthKind != mcp.AuthBearer {
		t.Fatalf("stored: %+v", u)
	}
	// reads are on, writes are off, the upload is skipped
	if n := a.MCP.Upstreams.OpenAPIToolCount("notes"); n != 2 {
		t.Fatalf("default tools = %d", n)
	}
	_, page := br.get("/admin/upstreams/notes/tools")
	for _, want := range []string{`data-toolpick`, `data-toolcount`, `class="toolcount ok"`, `Fewer tools work better`, `data-verbgroup="GET"`, `data-verbgroup="POST"`, `data-verbgroup="DELETE"`,
		`data-verb-toggle`, `data-toolfilter`, `skipped: file upload or binary body`, `Up to 15 is good, up to 30 is a lot`} {
		if !strings.Contains(page, want) {
			t.Errorf("tools page lacks %q", want)
		}
	}
	if strings.Contains(page, "SECRETTOKEN123") {
		t.Error("the secret is on the tools page")
	}
	if !regexp.MustCompile(`name="on" value="GET /notes" data-tool-on checked`).MatchString(page) || regexp.MustCompile(`name="on" value="POST /notes" data-tool-on checked`).MatchString(page) {
		t.Error("read operations must start on and write operations off")
	}
	// the list shows the counter pill, the type, and no secret
	_, list := br.get("/admin/upstreams")
	if !strings.Contains(list, `2 tools`) || !strings.Contains(list, `OpenAPI`) || strings.Contains(list, "SECRETTOKEN123") || !strings.Contains(list, `/admin/upstreams/notes/tools`) {
		t.Errorf("list: %s", list[:min(len(list), 3000)])
	}
	// the secret is sealed at rest
	var raw string
	a.DB.QueryRow(`SELECT auth_value FROM upstreams WHERE alias='notes'`).Scan(&raw)
	if raw == "" || strings.Contains(raw, "SECRETTOKEN123") {
		t.Errorf("stored secret %q", raw)
	}

	// save a selection: turn the delete on, rename the list tool, describe another
	save := url.Values{"csrf": {csrf}, "on": {"GET /notes", "DELETE /notes/{id}"},
		"key":  {"GET /notes", "POST /notes", "GET /notes/{id}", "DELETE /notes/{id}"},
		"name": {"find_notes", "addNote", "getNote", "deleteNote"},
		"desc": {"Lists my notes.", "Add a note", "", ""}}
	r, _ = br.post("/admin/upstreams/notes/tools/save", save)
	if r.StatusCode != 303 || flashKind(r) != "ok" {
		t.Fatalf("save: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	st, _ := a.MCP.Upstreams.OpenAPI("notes")
	var names []string
	for _, tl := range st.Tools {
		names = append(names, tl.Name+"|"+tl.Description)
	}
	if got := strings.Join(names, ","); got != "find_notes|Lists my notes.,deleteNote|DELETE /notes/{id}" {
		t.Fatalf("tools = %s", got)
	}
	// an invalid name is refused with the page kept
	save.Set("name", "bad name!")
	save["name"] = []string{"bad name!", "addNote", "getNote", "deleteNote"}
	r, _ = br.post("/admin/upstreams/notes/tools/save", save)
	if flashKind(r) != "bad" {
		t.Errorf("invalid name accepted: %q", flashKind(r))
	}
	// a skipped or unknown key is never enabled
	save["name"] = []string{"find_notes", "addNote", "getNote", "deleteNote"}
	save["on"] = []string{"POST /files", "GET /nope"}
	br.post("/admin/upstreams/notes/tools/save", save)
	if n := a.MCP.Upstreams.OpenAPIToolCount("notes"); n != 0 {
		t.Errorf("unknown keys exposed %d tools", n)
	}
	_, list = br.get("/admin/upstreams")
	if !strings.Contains(list, "no tools") {
		t.Error("a list row with no tools says so")
	}

	// end to end: an MCP client calls a tool and skgate makes the HTTP call with the stored credential
	br.post("/admin/upstreams/notes/tools/save", url.Values{"csrf": {csrf}, "on": {"GET /notes/{id}"}, "key": {"GET /notes/{id}"}, "name": {"getNote"}, "desc": {""}})
	key, _, _ := a.Keys.Create("t")
	req, _ := http.NewRequest("POST", br.ts.URL+"/mcp/notes", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getNote","arguments":{"id":"a/b"}}}`))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	txt := fmt.Sprint(out["result"])
	if !strings.Contains(txt, `GET /v1/notes/a/b`) || !strings.Contains(txt, "Bearer SECRETTOKEN123") {
		t.Errorf("call result: %v", out)
	}
}

func TestOpenAPIAddPasteFormatsAndErrors(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	a.Admin.NoSaveTest = true
	base := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "enabled": {"1"}}
	add := func(alias string, extra url.Values) *http.Response {
		v := url.Values{}
		for k, x := range base {
			v[k] = x
		}
		v.Set("alias", alias)
		for k, x := range extra {
			v[k] = x
		}
		r, _ := br.post("/admin/upstreams/save", v)
		return r
	}
	yaml := "openapi: 3.0.0\ninfo:\n  title: Y\n  version: '1'\nservers:\n  - url: https://y.example.com/api\npaths:\n  /a:\n    get:\n      operationId: a\n"
	toml := "openapi = \"3.1.0\"\n[info]\ntitle = \"T\"\nversion = \"1\"\n[paths.\"/a\".get]\noperationId = \"a\"\n"
	sw := `{"swagger":"2.0","info":{"title":"S","version":"1"},"host":"s.example.com","basePath":"/v2","schemes":["https"],"paths":{"/a":{"get":{"operationId":"a"}}}}`
	for alias, c := range map[string]struct{ spec, base string }{"yamlapi": {yaml, "https://y.example.com/api"}, "swaggerapi": {sw, "https://s.example.com/v2"}, "tomlapi": {toml, "https://given.example.com"}} {
		extra := url.Values{"oa_spec_text": {c.spec}}
		if alias == "tomlapi" {
			extra.Set("oa_url", c.base) // no server in the description: the base URL is given
		}
		r := add(alias, extra)
		if r.StatusCode != 303 || flashKind(r) != "ok" {
			t.Fatalf("%s: %d %q", alias, r.StatusCode, flashKind(r))
		}
		u, _ := a.MCP.Upstreams.Get(alias)
		if u.URL != c.base || a.MCP.Upstreams.OpenAPIToolCount(alias) != 1 {
			t.Errorf("%s: url %q tools %d", alias, u.URL, a.MCP.Upstreams.OpenAPIToolCount(alias))
		}
	}
	// refusals leave nothing behind
	for name, extra := range map[string]url.Values{
		"nothing given":      {},
		"garbage":            {"oa_spec_text": {"just some words: ["}},
		"no server, no base": {"oa_spec_text": {toml}},
		"query without name": {"oa_spec_text": {yaml}, "oa_auth_as": {"?"}, "oa_auth_value": {"k"}},
		"file URL":           {"oa_spec_url": {"file:///etc/passwd"}},
	} {
		r := add("bad-"+strings.ReplaceAll(strings.ReplaceAll(name, " ", ""), ",", ""), extra)
		if flashKind(r) != "bad" || !strings.HasSuffix(r.Header.Get("Location"), "/admin/upstreams/new") {
			t.Errorf("%s: %d %q %s", name, r.StatusCode, flashKind(r), r.Header.Get("Location"))
		}
	}
	list, _ := a.MCP.Upstreams.List()
	for _, u := range list {
		if strings.HasPrefix(u.Alias, "bad-") {
			t.Errorf("%s was stored", u.Alias)
		}
	}
}

func TestOpenAPIEditKeepsSecretAndDescription(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	api := oaAPI(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"},
		"oa_spec_url": {api.URL + "/spec.json"}, "oa_auth_as": {"basic"}, "oa_auth_value": {"alice:hunter2hunter2"}})
	_, page := br.get("/admin/upstreams/notes/edit")
	for _, want := range []string{`name="oa_spec_url"`, api.URL + "/spec.json", `name="oa_spec_text"`, `Empty keeps Notes API`, `name="oa_auth_as" value="basic"`, "ter2"} {
		if !strings.Contains(page, want) {
			t.Errorf("edit page lacks %q", want)
		}
	}
	if strings.Contains(page, "hunter2hunter2") {
		t.Error("the password is on the edit page")
	}
	// save without a new description or password: both are kept
	r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"edit"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"},
		"oa_url": {api.URL + "/v1"}, "oa_auth_as": {"basic"}, "oa_spec_url": {api.URL + "/spec.json"}})
	if r.StatusCode != 303 || flashKind(r) != "ok" || r.Header.Get("Location") != "/admin/upstreams" {
		t.Fatalf("edit: %d %s %q", r.StatusCode, r.Header.Get("Location"), flashKind(r))
	}
	u, _ := a.MCP.Upstreams.Get("notes")
	if u.AuthValue != "hunter2hunter2" || a.MCP.Upstreams.OpenAPIToolCount("notes") != 2 {
		t.Errorf("kept: %q tools %d", u.AuthValue, a.MCP.Upstreams.OpenAPIToolCount("notes"))
	}
	// the type cannot change
	r, _ = br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"edit"}, "kind": {"remote"}, "alias": {"notes"}, "url": {"https://x.example.com"}, "auth_kind": {"none"}})
	if flashKind(r) != "bad" {
		t.Error("type change accepted")
	}
}

func TestOpenAPICheckAndRepairNeverSaveAndNeedApproval(t *testing.T) {
	r := newSuggestRig(t, true, true)
	broken := `{"openapi":"3.0.0","info":{"title":"Broken","version":"1"},"paths":{"/a":{"get":{"summary":"x"}},"/b":{"get":{"summary":"y"}}}}`
	post := func(path string, v url.Values) (int, map[string]any) {
		v.Set("csrf", r.csrf)
		resp, body := r.br.post(path, v)
		var m map[string]any
		json.Unmarshal([]byte(body), &m)
		return resp.StatusCode, m
	}
	st, m := post("/admin/openapi/check", url.Values{"oa_spec_text": {broken}})
	if st != 200 || m["ok"] != true || m["title"] != "Broken" || len(m["issues"].([]any)) != 2 || m["ai"] != true {
		t.Fatalf("check: %d %v", st, m)
	}
	if st, m = post("/admin/openapi/check", url.Values{"oa_spec_text": {"[[["}}); m["ok"] != false || m["error"] == "" {
		t.Errorf("unreadable: %v", m)
	}
	r.reply = `{"patches":[{"op":"set","path":"/paths/~1a/get/operationId","value":"getA","reason":"missing id"},{"op":"set","path":"/paths/~1b/get/operationId","value":"getB"}]}`
	st, m = post("/admin/openapi/repair", url.Values{"oa_spec_text": {broken}})
	if st != 200 || len(m["changes"].([]any)) != 2 || len(m["remaining"].([]any)) != 0 || !strings.Contains(m["spec"].(string), `"operationId": "getA"`) {
		t.Fatalf("repair: %d %v", st, m)
	}
	// the prompt is its own: about OpenAPI repair, not the MCP server suggestion
	r.mu.Lock()
	prompt := r.prompt[len(r.prompt)-1]
	r.mu.Unlock()
	if !strings.Contains(prompt, "You repair OpenAPI documents") || strings.Contains(prompt, "Allowed commands") {
		t.Errorf("prompt: %.300s", prompt)
	}
	// nothing was stored
	if list, _ := r.a.MCP.Upstreams.List(); len(list) != 0 {
		t.Errorf("repair stored an upstream: %v", list)
	}
	// a repair that cannot apply is refused, not half-applied
	r.reply = `{"patches":[{"op":"set","path":"/nope/deeper/x","value":1}]}`
	if st, m = post("/admin/openapi/repair", url.Values{"oa_spec_text": {broken}}); st != 502 || m["error"] == nil {
		t.Errorf("bad patch: %d %v", st, m)
	}
	// a valid description has nothing to repair
	if st, _ = post("/admin/openapi/repair", url.Values{"oa_spec_text": {strings.ReplaceAll(strings.ReplaceAll(broken, `"summary":"x"`, `"operationId":"x"`), `"summary":"y"`, `"operationId":"y"`)}}); st != 400 {
		t.Errorf("valid: %d", st)
	}
}

func TestOpenAPISuggestNamesFillsAndSavesNothing(t *testing.T) {
	r := newSuggestRig(t, true, true)
	api := oaAPI(t)
	r.br.post("/admin/upstreams/save", url.Values{"csrf": {r.csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"}, "oa_spec_url": {api.URL + "/spec.json"}})
	r.reply = `{"tools":[{"key":"GET /notes","name":"list_my_notes","description":"Lists every note."},{"key":"POST /notes","name":"ignored","description":"x"}]}`
	resp, body := r.br.post("/admin/upstreams/notes/tools/suggest", url.Values{"csrf": {r.csrf}, "key": {"GET /notes"}})
	var m map[string]map[string]map[string]string
	json.Unmarshal([]byte(body), &m)
	if resp.StatusCode != 200 || m["tools"]["GET /notes"]["name"] != "list_my_notes" || m["tools"]["POST /notes"] != nil {
		t.Fatalf("suggest: %d %s", resp.StatusCode, body)
	}
	st, _ := r.a.MCP.Upstreams.OpenAPI("notes")
	if st.Tools[0].Name != "listNotes" {
		t.Errorf("suggesting saved a name: %q", st.Tools[0].Name)
	}
	if resp, _ = r.br.post("/admin/upstreams/notes/tools/suggest", url.Values{"csrf": {r.csrf}}); resp.StatusCode != 400 {
		t.Errorf("no tools ticked: %d", resp.StatusCode)
	}
	// the assistant is optional: without a ready model the page says why
	r2 := newSuggestRig(t, false, false)
	r2.br.post("/admin/upstreams/save", url.Values{"csrf": {r2.csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"}, "oa_spec_url": {api.URL + "/spec.json"}})
	_, page := r2.br.get("/admin/upstreams/notes/tools")
	if strings.Contains(page, "data-tool-describe") || !strings.Contains(page, "Suggest names:") {
		t.Error("without a model the page offers no suggestion button and says why")
	}
}

func TestOpenAPIManyToolsAreShownNotBlocked(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	a.Admin.NoSaveTest = true
	var paths []string
	for i := 0; i < 40; i++ {
		paths = append(paths, fmt.Sprintf(`"/r%d":{"get":{"operationId":"get%d"}}`, i, i))
	}
	spec := `{"openapi":"3.0.0","info":{"title":"Big","version":"1"},"servers":[{"url":"https://big.example.com"}],"paths":{` + strings.Join(paths, ",") + `}}`
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"big"}, "enabled": {"1"}, "oa_spec_text": {spec}})
	if n := a.MCP.Upstreams.OpenAPIToolCount("big"); n != 0 {
		t.Fatalf("with more reads than a model handles, none start on: %d", n)
	}
	var on, keys, names, descs []string
	for i := 0; i < 40; i++ {
		k := fmt.Sprintf("GET /r%d", i)
		on, keys, names, descs = append(on, k), append(keys, k), append(names, fmt.Sprintf("get%d", i)), append(descs, "")
	}
	r, _ := br.post("/admin/upstreams/big/tools/save", url.Values{"csrf": {csrf}, "on": on, "key": keys, "name": names, "desc": descs})
	if r.StatusCode != 303 || flashKind(r) != "ok" {
		t.Fatalf("a red count must still save: %d %q", r.StatusCode, flashKind(r))
	}
	_, msg := flashOf(r)
	if !strings.Contains(msg, "40 tools exposed") || !strings.Contains(msg, "too many") {
		t.Errorf("toast: %q", msg)
	}
	_, page := br.get("/admin/upstreams/big/tools")
	if !strings.Contains(page, `class="toolcount bad"`) || !strings.Contains(page, `class="callout bad"`) {
		t.Error("tools page must show the red level")
	}
	_, list := br.get("/admin/upstreams")
	if !regexp.MustCompile(`pill bad[^>]*>40 tools<`).MatchString(list) {
		t.Errorf("list must show a red 40 tools pill: %s", list[:min(len(list), 2500)])
	}
	_, status := br.get("/admin")
	if !strings.Contains(status, "OpenAPI tools") || !regexp.MustCompile(`pill bad[^>]*>40 tools<`).MatchString(status) {
		t.Error("status overview must show the total")
	}
}

func TestOpenAPIUpstreamTestAndTrailingSlash(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	api := oaAPI(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"}, "oa_spec_url": {api.URL + "/spec.json"}})
	_, body := br.get("/admin/upstreams/notes/test")
	if !strings.Contains(body, "listNotes") || !strings.Contains(body, "OK") {
		t.Errorf("test page: %s", body[:min(len(body), 2500)])
	}
	for _, p := range []string{"/admin/upstreams/notes/tools", "/admin/upstreams/notes/tools/", "/admin/upstreams/notes/edit/"} {
		if r, _ := br.get(p); r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	// a remote upstream has no tools page
	a.MCP.Upstreams.Create(mcp.Upstream{Alias: "plain", URL: "http://127.0.0.1:1/mcp", AuthKind: mcp.AuthNone, Enabled: true})
	if r, _ := br.get("/admin/upstreams/plain/tools"); r.StatusCode != 303 || flashKind(r) != "bad" {
		t.Errorf("remote tools page: %d", r.StatusCode)
	}
	// deleting removes the description too
	br.post("/admin/upstreams/notes/delete", url.Values{"csrf": {csrf}})
	if _, err := a.MCP.Upstreams.OpenAPI("notes"); err == nil {
		t.Error("description kept after delete")
	}
}

// The tools page and the form, in a real browser: the live counter and its colors, the toasts, the verb switches,
// the filter, and the check/repair flow with its approval step.
func TestOpenAPIToolPickerInBrowser(t *testing.T) {
	r := newSuggestRig(t, true, true)
	r.a.Admin.NoSaveTest = true
	var paths []string
	for i := 0; i < 20; i++ {
		paths = append(paths, fmt.Sprintf(`"/item%d":{"get":{"operationId":"getItem%d"},"delete":{"operationId":"deleteItem%d"}}`, i, i, i))
	}
	spec := `{"openapi":"3.0.0","info":{"title":"Items","version":"1"},"servers":[{"url":"https://items.example.com"}],"paths":{` + strings.Join(paths, ",") + `}}`
	// 20 reads is under the limit at which none start on, so switch them off to begin at zero
	r.br.post("/admin/upstreams/save", url.Values{"csrf": {r.csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"items"}, "enabled": {"1"}, "oa_spec_text": {spec}})
	st, _ := r.a.MCP.Upstreams.OpenAPI("items")
	cfg := st.Config
	cfg.Selection.Enabled = map[string]bool{}
	if err := r.a.MCP.Upstreams.SetOpenAPI("items", cfg); err != nil {
		t.Fatal(err)
	}
	r.reply = `{"patches":[{"op":"set","path":"/paths/~1a/get/operationId","value":"getA","reason":"missing id"}]}`
	runBrowserScript(t, "toolpick.js", r.br.ts.URL, r.br, "items")
}

// Small corrections to what the OpenAPI pages say: the test page shows no HTTP exchange that never happened,
// "read again" needs an address, only GET and HEAD start on, and an oversized form is told so.
func TestOpenAPIPagesSayOnlyWhatIsTrue(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	a.Admin.NoSaveTest = true
	api := oaAPI(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"byurl"}, "enabled": {"1"}, "oa_spec_url": {api.URL + "/spec.json"}})
	spec := `{"openapi":"3.0.0","info":{"title":"P"},"servers":[{"url":"https://p.example.com"}],"paths":{"/a":{"get":{"operationId":"a"},"options":{"operationId":"o"},"trace":{"operationId":"tr"},"head":{"operationId":"h"}}}}`
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"pasted"}, "enabled": {"1"}, "oa_spec_text": {spec}})

	_, test := br.get("/admin/upstreams/byurl/test")
	for _, bad := range []string{"HTTP status", "no response", "(mode", "(protocol )"} {
		if strings.Contains(test, bad) {
			t.Errorf("test page says %q for an OpenAPI upstream", bad)
		}
	}
	// Reading the address again is the Update button on the tools page: live for an address, off with a reason for pasted text.
	_, withURL := br.get("/admin/upstreams/byurl/tools")
	_, noURL := br.get("/admin/upstreams/pasted/tools")
	if !strings.Contains(withURL, "/admin/upstreams/byurl/spec/update") || strings.Contains(noURL, "/admin/upstreams/pasted/spec/update") ||
		!strings.Contains(strings.ReplaceAll(noURL, "&#39;", "'"), "no address to update from") {
		t.Error("Update belongs to an upstream that has an address, and only to it")
	}
	if n := a.MCP.Upstreams.OpenAPIToolCount("pasted"); n != 2 { // GET and HEAD; OPTIONS and TRACE start off
		t.Errorf("default tools = %d, want 2", n)
	}
	big := url.Values{"csrf": {csrf}, "name": {strings.Repeat("x", 1<<20+10)}}
	if r, body := br.post("/admin/keys/create", big); r.StatusCode != 413 || !strings.Contains(body, "too large") {
		t.Errorf("oversized form: %d %.100q", r.StatusCode, body)
	}
}

func TestOpenAPIAddByBaseAddressFindsTheDescription(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" { // the description names this machine, as many do
			fmt.Fprint(w, `{"openapi":"3.0.3","info":{"title":"Meals","version":"1"},"servers":[{"url":"http://localhost:9000"}],"paths":{"/recipes":{"get":{"operationId":"list","responses":{"200":{"description":"ok"}}}}}}`)
			return
		}
		if r.URL.Path == "/recipes" {
			fmt.Fprint(w, `[]`)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"meals"}, "enabled": {"1"},
		"oa_spec_url": {ts.URL}})
	if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/upstreams/meals/tools" {
		t.Fatalf("add: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	u, _ := a.MCP.Upstreams.Get("meals")
	st, err := a.MCP.Upstreams.OpenAPI("meals")
	if err != nil || st.Config.SpecURL != ts.URL+"/openapi.json" || u.URL != ts.URL {
		t.Fatalf("stored spec_url %q base %q: %v", st.Config.SpecURL, u.URL, err)
	}
	// nothing there: one plain sentence, nothing saved
	empty := httptest.NewServer(http.NotFoundHandler())
	defer empty.Close()
	r, _ = br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"none"}, "enabled": {"1"},
		"oa_spec_url": {empty.URL}})
	if _, ok := a.MCP.Upstreams.Get("none"); ok {
		t.Error("saved without a description")
	}
	_ = r
}

func TestSuggestRoutesAnAPIAddressToTheOpenAPIForm(t *testing.T) {
	r := newSuggestRig(t, true, false) // no helper model: an API address needs none
	api := oaAPI(t)
	resp, got := r.postJSON("/admin/upstreams/suggest", url.Values{"source": {api.URL + "/spec.json"}})
	if resp.StatusCode != 200 || got["kind"] != "openapi" || got["spec_url"] != api.URL+"/spec.json" || got["title"] != "Notes API" || got["alias"] != "api" {
		t.Fatalf("%d %v", resp.StatusCode, got)
	}
	empty := httptest.NewServer(http.NotFoundHandler())
	defer empty.Close()
	resp, got = r.postJSON("/admin/upstreams/suggest", url.Values{"source": {empty.URL}})
	msg, _ := got["error"].(string)
	if resp.StatusCode != 422 || !strings.Contains(msg, "paste it") {
		t.Fatalf("nothing found: %d %v", resp.StatusCode, got)
	}
	resp, got = r.postJSON("/admin/upstreams/suggest", url.Values{"source": {"http://api.example.com/openapi.json"}})
	msg, _ = got["error"].(string)
	if resp.StatusCode != 422 || !strings.Contains(msg, "https") {
		t.Fatalf("public http: %d %v", resp.StatusCode, got)
	}
}

// A bearer-protected API that serves its description openly.
func keyedAPI(t *testing.T, good string) (*httptest.Server, string) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" || r.Header.Get("Authorization") == "Bearer "+good {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)
	spec := fmt.Sprintf(`{"openapi":"3.0.0","info":{"title":"Keyed","version":"1"},"servers":[{"url":"%s"}],"security":[{"b":[]}],`+
		`"components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}},"paths":{"/ping":{"get":{"operationId":"ping"}}}}`, ts.URL)
	return ts, spec
}

func TestOpenAPIAddIsTestedFirstAndAFailedAddKeepsTheForm(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	api, spec := keyedAPI(t, "GOOD")
	form := func(key string, extra url.Values) url.Values {
		v := url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"keyed"}, "enabled": {"1"}, "oa_spec_text": {spec}, "oa_auth_value": {key}}
		for k, x := range extra {
			v[k] = x
		}
		return v
	}
	// the page script posts in place and asks to be sent on after a good save
	post := func(v url.Values) (int, map[string]any, *http.Response) {
		req, _ := http.NewRequest("POST", br.ts.URL+"/admin/upstreams/save", strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Follow", "1")
		resp, err := br.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out, resp
	}
	st, out, _ := post(form("WRONG", nil))
	toast, _ := out["toast"].(map[string]any)
	if st != 200 || toast["k"] != "bad" || toast["m"] != "the server refused the key" || out["to"] != nil {
		t.Fatalf("refused key: %d %v", st, out)
	}
	if _, ok := a.MCP.Upstreams.Get("keyed"); ok {
		t.Fatal("stored although the test failed")
	}
	st, out, resp := post(form("GOOD", nil))
	toast, _ = out["toast"].(map[string]any)
	if st != 200 || toast["k"] != "ok" || out["to"] != "/admin/upstreams/keyed/tools" {
		t.Fatalf("good key: %d %v", st, out)
	}
	if k, _ := flashOf(resp); k != "ok" {
		t.Error("the next page shows the toast")
	}
	u, _ := a.MCP.Upstreams.Get("keyed")
	if u.AuthKind != mcp.AuthAuto || u.DetectedKind != "bearer" || u.AuthValue != "GOOD" || u.URL != api.URL {
		t.Fatalf("stored: %+v", u)
	}
	// an edit keeps the stored key when the field is empty, and tests again
	r, _ := br.post("/admin/upstreams/keyed/save", form("", url.Values{"mode": {"edit"}}))
	if r.StatusCode != 303 || flashKind(r) != "ok" {
		t.Fatalf("edit: %d %q", r.StatusCode, flashKind(r))
	}
	if u, _ := a.MCP.Upstreams.Get("keyed"); u.AuthValue != "GOOD" {
		t.Errorf("key kept: %q", u.AuthValue)
	}
}

func TestOpenAPIAdvancedLineSetsTheWayToSendTheKey(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	a.Admin.NoSaveTest = true
	_, spec := keyedAPI(t, "x")
	for _, c := range []struct{ as, value, kind, name string }{
		{"", "K", mcp.AuthAuto, ""}, {"bearer", "K", mcp.AuthBearer, ""}, {"X-Custom-Key", "K", mcp.AuthHeader, "X-Custom-Key"},
		{"Authorization", "Token K", mcp.AuthHeader, "Authorization"}, {"?api_key", "K", mcp.AuthQuery, "api_key"}, {"basic", "al:pw", mcp.AuthBasic, "al"}, {"", "", mcp.AuthNone, ""},
	} {
		alias := "adv-" + strings.ToLower(strings.NewReplacer("?", "q", ":", "-", " ", "-").Replace(c.kind+c.name+c.as))
		r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {strings.ReplaceAll(alias, "_", "-")}, "enabled": {"1"},
			"oa_spec_text": {spec}, "oa_auth_value": {c.value}, "oa_auth_as": {c.as}})
		u, ok := a.MCP.Upstreams.Get(strings.ReplaceAll(alias, "_", "-"))
		if !ok || u.AuthKind != c.kind || u.AuthName != c.name {
			t.Errorf("as %q value %q: %d %q stored %+v", c.as, c.value, r.StatusCode, flashKind(r), u)
		}
	}
	// the stored way shows on the edit page without the key
	r, _ := br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"showas"}, "enabled": {"1"},
		"oa_spec_text": {spec}, "oa_auth_value": {"SECRETKEY99"}, "oa_auth_as": {"X-Custom-Key"}})
	_ = r
	_, page := br.get("/admin/upstreams/showas/edit")
	if !strings.Contains(page, `name="oa_auth_as" value="X-Custom-Key"`) || strings.Contains(page, "SECRETKEY99") || strings.Contains(page, `name="oa_auth_kind"`) {
		t.Error("edit page: the way shows, the key does not, and there is no auth select")
	}
}

func TestAddFormInBrowser(t *testing.T) {
	a, _, br, _ := signedIn(t, nil)
	_ = a
	api, spec := keyedAPI(t, "GOODKEY")
	runBrowserScript(t, "addfail.js", br.ts.URL, br, api.URL, spec)
}

// The OpenAPI screens use the plain voice of the rest of the app: no filler words, no em dashes, no parentheses in
// hints, and hints of one short sentence.
func TestOpenAPIScreensUseThePlainVoice(t *testing.T) {
	r := newSuggestRig(t, true, true)
	api := oaAPI(t)
	r.br.post("/admin/upstreams/save", url.Values{"csrf": {r.csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"}, "oa_spec_url": {api.URL + "/spec.json"}})
	_, review := r.br.post("/admin/upstreams/notes/spec/update", url.Values{"csrf": {r.csrf}})
	pages := map[string]string{"review": review}
	for _, p := range []string{"/admin/upstreams/new", "/admin/upstreams/notes/edit", "/admin/upstreams/notes/tools", "/admin/upstreams/notes/test"} {
		_, pages[p] = r.br.get(p)
	}
	var code []string // the script without its comment lines
	for _, l := range strings.Split(readFile(t, "../admin/static/app.js"), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			code = append(code, l)
		}
	}
	pages["app.js"] = strings.Join(code, "\n")
	banned := []string{"leverage", "seamless", "powerful", "helpful", "Let's", "let's", "\u2014", "\u2013", "simply", "easily", "assistant"}
	muted := regexp.MustCompile(`(?s)<p class="muted"[^>]*>(.*?)</p>`)
	tags := regexp.MustCompile(`<[^>]+>`)
	oaGroup := regexp.MustCompile(`(?s)<div class="group" data-kind="openapi">.*?<div class="group"`)
	for name, page := range pages {
		if strings.HasPrefix(name, "/admin/upstreams/new") || strings.HasSuffix(name, "/edit") {
			page = oaGroup.FindString(page) // the other types have their own copy
			if page == "" {
				t.Fatalf("%s: no OpenAPI group", name)
			}
		}
		for _, w := range banned {
			if strings.Contains(page, w) {
				t.Errorf("%s: %q does not belong in the copy", name, w)
			}
		}
		if strings.HasSuffix(name, ".js") {
			continue
		}
		for _, m := range muted.FindAllStringSubmatch(page, -1) {
			txt := strings.TrimSpace(tags.ReplaceAllString(m[1], ""))
			if strings.Contains(txt, "(") || len(txt) > 90 || strings.Count(txt, ". ") > 0 {
				t.Errorf("%s: hint is not short and plain: %q", name, txt)
			}
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
