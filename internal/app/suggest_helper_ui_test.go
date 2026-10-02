package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
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
	_, page := out.br.get("/admin/upstreams")
	if !strings.Contains(page, `disabled title="sign in on the status page"`) || strings.Contains(page, opener) {
		t.Error("signed out: expected the sign-in explanation and no picker")
	}

	none := newSuggestRig(t, true, false)
	_, page = none.br.get("/admin/upstreams")
	for _, want := range []string{`disabled title="pick an MCP helper model first"`, opener, "Pick MCP helper model",
		`<template data-dialog-content id="helper-model" data-title="MCP helper model">`, `action="/admin/providers/grok/model"`, `data-inline=""`, `<option value="helper-2"`} {
		if !strings.Contains(page, want) {
			t.Errorf("no model: page lacks %q", want)
		}
	}

	with := newSuggestRig(t, true, true)
	_, page = with.br.get("/admin/upstreams")
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
		_, page := tc.rig.br.get("/admin/upstreams")
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
	_, edit := r.br.get("/admin/upstreams/edit?alias=ed")
	if strings.Contains(edit, `<select name="kind"`) || !strings.Contains(edit, `<input type="hidden" name="kind" value="stdio"`) {
		t.Error("edit page must not offer a type")
	}
}
