package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// postJSON posts like the page script does: Accept application/json, answered in place.
func (r *suggestRig) postJSON(path string, v url.Values) (*http.Response, map[string]any) {
	v.Set("csrf", r.csrf)
	req, _ := http.NewRequest("POST", r.br.ts.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := r.br.c.Do(req)
	if err != nil {
		r.br.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp, m
}

// With an account but no MCP helper model, the upstream page offers the model dialog next to the
// disabled Suggest button; signed out it only explains, and with a model it shows the choice.
func TestUpstreamPageOffersHelperModelPicker(t *testing.T) {
	const opener = `data-dialog-open="#helper-model"`
	out := newSuggestRig(t, false, false)
	_, page := out.br.get("/admin/upstreams/new")
	if !strings.Contains(page, `disabled title="sign in on the status page"`) || strings.Contains(page, opener) {
		t.Error("signed out: expected the sign-in explanation and no picker")
	}

	none := newSuggestRig(t, true, false)
	_, page = none.br.get("/admin/upstreams/new")
	for _, want := range []string{`disabled title="pick an MCP helper model first"`, opener, "Pick MCP helper model",
		`<template data-dialog-content id="helper-model" data-title="MCP helper model">`, `action="/admin/providers/grok/model"`, `data-inline=""`, `<option value="helper-2"`} {
		if !strings.Contains(page, want) {
			t.Errorf("no model: page lacks %q", want)
		}
	}

	with := newSuggestRig(t, true, true)
	_, page = with.br.get("/admin/upstreams/new")
	if strings.Contains(page, `data-suggest="/admin/upstreams/suggest" disabled`) || !strings.Contains(page, "MCP helper model: helper-2") {
		t.Error("with a model: Suggest must be enabled and the model shown")
	}

	// the status page dialog keeps the plain form (full page post)
	_, status := none.br.get("/admin")
	if strings.Contains(status, `data-inline`) || strings.Contains(status, "Pick MCP helper model") {
		t.Error("the status page dialog must not use the inline picker")
	}
}

// Picking the model from the upstream page answers in place: a toast and the re-rendered controls
// (Suggest enabled), with nothing queued as a flash or redirect.
func TestHelperModelPickedInPlace(t *testing.T) {
	r := newSuggestRig(t, true, false)
	resp, bad := r.postJSON("/admin/providers/grok/model", url.Values{"model": {"nope"}})
	if resp.StatusCode != 200 || bad["toast"].(map[string]any)["k"] != "bad" || !strings.Contains(bad["html"].(string), `disabled title="pick an MCP helper model first"`) {
		t.Fatalf("unlisted model: %d %v", resp.StatusCode, bad)
	}
	resp, ok := r.postJSON("/admin/providers/grok/model", url.Values{"model": {"helper-2"}})
	tt := ok["toast"].(map[string]any)
	html := ok["html"].(string)
	if resp.StatusCode != 200 || tt["k"] != "ok" || tt["m"] != "MCP helper model: helper-2" {
		t.Fatalf("pick: %d %v", resp.StatusCode, ok)
	}
	if !strings.HasPrefix(html, `<div class="row" data-suggest-controls>`) || strings.Contains(html, `data-suggest="/admin/upstreams/suggest" disabled`) ||
		!strings.Contains(html, "MCP helper model: helper-2") || !strings.Contains(html, `<option value="helper-2" selected>`) {
		t.Fatalf("controls not re-rendered:\n%s", html)
	}
	if resp.Header.Get("Location") != "" || flashKind(resp) != "" {
		t.Error("an in-place answer must not redirect or queue a flash")
	}
	if v, _ := r.a.DB.GetSetting("provider.grok.model"); v != "helper-2" {
		t.Fatalf("stored %q", v)
	}
	// Suggest now runs
	if st, _ := r.suggest(url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}}); st != 200 {
		t.Fatalf("suggest after picking: %d", st)
	}
	// reload keeps working in place
	_, rl := r.postJSON("/admin/providers/grok/models/reload", url.Values{})
	if rl["toast"].(map[string]any)["m"] != "2 models loaded" || rl["html"] == "" {
		t.Fatalf("reload: %v", rl)
	}
}

func TestInlineFormScript(t *testing.T) {
	_, ts, _, _ := signedIn(t, nil)
	r, _ := http.Get(ts.URL + "/admin/static/app.js")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	for _, want := range []string{`"data-inline"`, "data-suggest-controls", "skgateToast"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

// The add form starts on the managed type only when Suggest can run (signed in with an MCP helper
// model); picking the model in place leaves the page as it is, and the edit page never preselects it.
func TestAddFormDefaultType(t *testing.T) {
	managed := regexp.MustCompile(`<option value="stdio"\s+selected`)
	remote := regexp.MustCompile(`<option value="remote"\s+selected`)
	for name, tc := range map[string]struct {
		rig     *suggestRig
		managed bool
	}{
		"signed out": {newSuggestRig(t, false, false), false},
		"no model":   {newSuggestRig(t, true, false), false},
		"ready":      {newSuggestRig(t, true, true), true},
	} {
		_, page := tc.rig.br.get("/admin/upstreams/new")
		if managed.MatchString(page) != tc.managed || remote.MatchString(page) == tc.managed {
			t.Errorf("%s: wrong default type (managed=%v)", name, tc.managed)
		}
	}
	// picking the model in place does not touch the type: the answer only carries the controls
	r := newSuggestRig(t, true, false)
	_, ok := r.postJSON("/admin/providers/grok/model", url.Values{"model": {"helper-2"}})
	if strings.Contains(ok["html"].(string), `name="kind"`) {
		t.Error("the in-place answer must not re-render the type")
	}
	// editing: the type is fixed, whatever the helper state
	form := stdioForm(r.csrf, "ed", url.Values{})
	if resp, _ := r.br.post("/admin/upstreams/save", form); flashKind(resp) != "ok" {
		t.Fatal("save failed")
	}
	_, edit := r.br.get("/admin/upstreams/ed/edit")
	if strings.Contains(edit, `<select name="kind"`) || !strings.Contains(edit, `<input type="hidden" name="kind" value="stdio"`) {
		t.Error("edit page must not offer a type")
	}
}

// The suggest endpoint logs the request and the reason of a refusal, never the token or the raw input.
func TestSuggestEndpointLogs(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r := newSuggestRig(t, true, true)
	r.suggest(url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
	r.suggest(url.Values{"source": {"https://user:" + tokenSecret + "@host.example/not a source"}})
	out := buf.String()
	for _, want := range []string{"suggest: request source=https://gitlab.com/grp/thing token=true", `suggest: start source=https://gitlab.com/grp/thing model="helper-2"`, "suggest: ok source=https://gitlab.com/grp/thing", "suggest: request refused HTTP 400"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	if strings.Contains(out, tokenSecret) {
		t.Error("the token reached the log")
	}
}

// A refused admin action is logged with its reason, whatever toast the user got.
func TestRefusedAdminActionIsLogged(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	_, _, br, csrf := signedIn(t, nil)
	br.post("/admin/providers/grok/settings", url.Values{"csrf": {csrf}, "base": {"bad"}})
	if !strings.Contains(buf.String(), "admin: POST /admin/providers/grok/settings refused: URLs must be absolute http(s)") {
		t.Errorf("not logged:\n%s", buf.String())
	}
}

// In the shared model picker the load time comes first and the Reload button follows, in every dialog that uses it.
func TestModelPickerShowsTimeBeforeReload(t *testing.T) {
	r := newSuggestRig(t, true, true)
	for _, path := range []string{"/admin", "/admin/upstreams/new"} {
		_, page := r.br.get(path)
		i, j := strings.Index(page, "2 models, loaded "), strings.Index(page, "Reload models")
		if i < 0 || j < 0 || i > j {
			t.Errorf("%s: time at %d, button at %d", path, i, j)
		}
	}
}

// The suggest endpoint streams stage lines and then the result (or the error) when asked for NDJSON; plain
// JSON callers are unchanged.
func TestSuggestStreamsStages(t *testing.T) {
	r := newSuggestRig(t, true, true)
	post := func(v url.Values) (*http.Response, []map[string]any) {
		v.Set("csrf", r.csrf)
		req, _ := http.NewRequest("POST", r.br.ts.URL+"/admin/upstreams/suggest", strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/x-ndjson")
		resp, body := r.br.do(req)
		var out []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) != nil {
				t.Fatalf("not a JSON line: %q", l)
			}
			out = append(out, m)
		}
		return resp, out
	}
	resp, lines := post(url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") || len(lines) != 5 {
		t.Fatalf("%s %v", resp.Header.Get("Content-Type"), lines)
	}
	for i, st := range []string{"fetch", "read", "model", "check"} {
		if lines[i]["stage"] != st {
			t.Errorf("line %d: %v", i, lines[i])
		}
	}
	if src := lines[1]["source"].(map[string]any); src["name"] != "grp/thing" {
		t.Errorf("source %v", src)
	}
	if res, ok := lines[4]["result"].(map[string]any); !ok || res["command"] != "node" {
		t.Errorf("result %v", lines[4])
	}
	// a failure arrives as an error line
	_, lines = post(url.Values{"source": {"https://gitlab.com/grp/thing"}})
	if last := lines[len(lines)-1]; last["error"] == nil || !strings.Contains(last["error"].(string), "token") {
		t.Errorf("error line %v", lines)
	}
	// the script and styles carry the stream reader and honor reduced motion
	_, js := r.br.get("/admin/static/app.js")
	_, css := r.br.get("/admin/static/app.css")
	if !strings.Contains(js, "application/x-ndjson") || !strings.Contains(css, "prefers-reduced-motion") || !strings.Contains(css, "reveal-in") {
		t.Error("stream reader or reveal style missing")
	}
}

// Both model dialogs carry the Reasoning dropdown directly under the model dropdown (same form, so Save covers
// both), defaulting to "auto" (the model decides). It is the only reasoning setting: there is none for chat.
func TestReasoningSelectorInModelDialogs(t *testing.T) {
	r := newSuggestRig(t, true, true)
	for _, path := range []string{"/admin", "/admin/upstreams/new"} {
		_, page := r.br.get(path)
		m, e, reload := strings.Index(page, `<select name="model"`), strings.Index(page, `<select name="effort"`), strings.Index(page, "Reload models")
		if m < 0 || e < m || reload < e {
			t.Errorf("%s: model select at %d, reasoning at %d, reload at %d", path, m, e, reload)
		}
		if !strings.Contains(page, `<option value="auto" selected>Reasoning: auto (model decides)</option>`) || !strings.Contains(page, "Reasoning: low") {
			t.Errorf("%s: reasoning options missing or not defaulting to auto", path)
		}
		if strings.Contains(page, "Effort") || strings.Contains(page, "effort</") || strings.Contains(page, "chat-effort") {
			t.Errorf("%s: the old effort wording or the chat setting is still there", path)
		}
	}
	_, css := r.br.get("/admin/static/app.css")
	if !strings.Contains(css, ".pick{display:grid") {
		t.Error("shared pick style missing")
	}

	ask := func() string {
		r.suggest(url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.prompt[len(r.prompt)-1]
	}
	if body := ask(); strings.Contains(body, "reasoning_effort") {
		t.Errorf("auto must send nothing: %.200s", body)
	}
	_, ok := r.postJSON("/admin/providers/grok/model", url.Values{"model": {"helper-2"}, "effort": {"high"}})
	if ok["toast"].(map[string]any)["m"] != "MCP helper model: helper-2, reasoning high" {
		t.Errorf("toast %v", ok["toast"])
	}
	if body := ask(); !strings.Contains(body, `"reasoning_effort":"high"`) {
		t.Errorf("stored reasoning not sent: %.200s", body)
	}
	r.postJSON("/admin/providers/grok/model", url.Values{"model": {"helper-2"}, "effort": {"auto"}})
	if body := ask(); strings.Contains(body, "reasoning_effort") {
		t.Errorf("auto must send nothing: %.200s", body)
	}
	if _, bad := r.postJSON("/admin/providers/grok/model", url.Values{"model": {"helper-2"}, "effort": {"extreme"}}); bad["toast"].(map[string]any)["k"] != "bad" {
		t.Error("an unknown reasoning choice must be refused")
	}
	// the chat setting is gone: its route answers 404 and nothing stored for it is read
	if resp, _ := r.br.post("/admin/providers/grok/chat-effort", url.Values{"csrf": {r.csrf}, "effort": {"medium"}}); resp.StatusCode != 404 {
		t.Errorf("chat-effort route: %d", resp.StatusCode)
	}
	// a choice saved by an older version keeps working
	_ = r.a.DB.SetSetting("provider.grok.effort", "medium")
	if got := r.a.Admin.Set.Effort("grok"); got != "medium" {
		t.Errorf("saved choice lost: %s", got)
	}
}

// A silent model ends the suggestion with a timed-out error that names the stage and the reasoning, in the log, the
// stream and the plain JSON answer; the page script shows the state and the button to lower the reasoning.
func TestSuggestTimeoutIsVisible(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r := newSuggestRig(t, true, true)
	r.setHang(true)
	r.a.Admin.SuggestIdle = 150 * time.Millisecond
	form := url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}, "csrf": {r.csrf}}

	req, _ := http.NewRequest("POST", r.br.ts.URL+"/admin/upstreams/suggest", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/x-ndjson")
	_, body := r.br.do(req)
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	to, _ := last["timeout"].(map[string]any)
	if to == nil || to["kind"] != "idle" || to["stage"] != "model" || to["where"] != "asking the model" || to["effort"] != "auto" {
		t.Fatalf("timeout object: %v", last)
	}
	if msg, _ := last["error"].(string); !strings.Contains(msg, "timed out while asking the model") || !strings.Contains(msg, "lower the reasoning") {
		t.Errorf("reason: %v", last["error"])
	}

	resp, plain := r.postJSON("/admin/upstreams/suggest", url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
	if resp.StatusCode != http.StatusGatewayTimeout || plain["timeout"] == nil {
		t.Errorf("plain answer: %d %v", resp.StatusCode, plain)
	}

	// the overall cap is reported as such
	r.a.Admin.SuggestIdle, r.a.Admin.SuggestCap = time.Minute, 200*time.Millisecond
	_, capped := r.postJSON("/admin/upstreams/suggest", url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
	if c, _ := capped["timeout"].(map[string]any); c == nil || c["kind"] != "cap" {
		t.Errorf("cap: %v", capped)
	}

	out := buf.String()
	for _, want := range []string{"suggest: model call timed out (idle) stage=model reasoning=auto", "suggest: model call timed out (cap)", "suggest: failed source=https://gitlab.com/grp/thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	if strings.Contains(out, tokenSecret) {
		t.Error("the token reached the log")
	}

	_, js := r.br.get("/admin/static/app.js")
	_, page := r.br.get("/admin/upstreams/new")
	if !strings.Contains(js, "Timed out while ") || !strings.Contains(js, `"Lower reasoning"`) || !strings.Contains(js, `"#helper-model"`) || !strings.Contains(page, `id="helper-model"`) {
		t.Error("the timed-out state or the Lower reasoning button is missing")
	}
}

// The page script turns a timeout into a distinct state with the elapsed time and the Lower reasoning button, and
// shows streamed activity while the model works. Needs node and jsdom (see TestModalBehaviorInJSDOM).
func TestSuggestTimeoutStateInJSDOM(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	env := os.Environ()
	if d := os.Getenv("SKGATE_JSDOM"); d != "" {
		env = append(env, "NODE_PATH="+filepath.Join(d, "node_modules"))
	}
	probe := exec.Command(node, "-e", `require("jsdom")`)
	probe.Env = env
	if err := probe.Run(); err != nil {
		t.Skip("jsdom is not available (set SKGATE_JSDOM to a directory with node_modules/jsdom)")
	}
	r := newSuggestRig(t, true, true)
	_, page := r.br.get("/admin/upstreams/new")
	dir := t.TempDir()
	file := filepath.Join(dir, "upstreams.html")
	if err := os.WriteFile(file, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	js, _ := filepath.Abs(filepath.Join("..", "admin", "static", "app.js"))
	script, _ := filepath.Abs(filepath.Join("testdata", "suggest_timeout.js"))
	cmd := exec.Command(node, script, file, js)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL OK") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The status page shows the helper model and its reasoning as one pill: "plain · reasoning auto", low, medium or high.
func TestStatusShowsReasoningPill(t *testing.T) {
	up, _ := aliasUpstream(t)
	a, _, br, csrf, _ := signedInProvider(t, up)
	br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
	row := func() string {
		_, page := br.get("/admin")
		i := strings.Index(page, "<th>MCP helper model</th>")
		return page[i : i+strings.Index(page[i:], "</tr>")]
	}
	if r := row(); strings.Contains(r, "reasoning") {
		t.Errorf("no model, no reasoning:\n%s", r)
	}
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"plain"}})
	if r := row(); !strings.Contains(r, ">plain \u00b7 reasoning auto</span>") {
		t.Errorf("unset reasoning shows auto:\n%s", r)
	}
	for _, v := range []string{"low", "medium", "high"} {
		br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"plain"}, "effort": {v}})
		if r := row(); !strings.Contains(r, ">plain \u00b7 reasoning "+v+"</span>") || strings.Contains(r, "auto") {
			t.Errorf("%s: %s", v, r)
		}
	}
	// a value stored under the earlier name reads as auto
	a.DB.SetSetting("provider.grok.effort", "default")
	if r := row(); !strings.Contains(r, ">plain \u00b7 reasoning auto</span>") {
		t.Errorf("stored default must read as auto:\n%s", r)
	}
}
