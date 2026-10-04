package app

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The helper model picker carries the timeout field, the note that capable models are slower, and a 600 s
// suggestion that appears only for heavy models and is never applied.
func TestHelperTimeoutSettingAndSuggestion(t *testing.T) {
	up, _ := modelsUpstream(t, "grok-4.7-reasoning", "grok-mini")
	a, _, br, csrf, _ := signedInProvider(t, up)
	post := func(v url.Values) string {
		v.Set("csrf", csrf)
		_, body := br.post("/admin/providers/grok/models/reload", v)
		return body
	}
	post(url.Values{})
	_, page := br.get("/admin")
	for _, want := range []string{`name="timeout" value="120"`, "Heavy reasoning models need about 600 seconds.",
		`<option value="grok-4.7-reasoning" data-frontier>`, `<option value="grok-mini">`, `data-frontier-hint="600"`} {
		if !strings.Contains(page, want) {
			t.Errorf("status page lacks %q", want)
		}
	}
	if strings.Contains(page, `<p class="muted" data-frontier-hint="600" >`) { // hidden unless the chosen model is heavy
		t.Error("the suggestion shows with no model chosen")
	}
	if got := a.Admin.Set.HelperTimeout("grok"); got != 120*time.Second {
		t.Fatalf("default %s", got)
	}

	// saving a heavy model does not touch the timeout; the suggestion is shown, not applied
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-4.7-reasoning"}})
	if got := a.Admin.Set.HelperTimeout("grok"); got != 120*time.Second {
		t.Fatalf("choosing a heavy model changed the timeout to %s", got)
	}
	_, page = br.get("/admin")
	if !strings.Contains(page, `data-frontier-hint="600" >`) && !strings.Contains(page, `data-frontier-hint="600">`) {
		t.Error("the suggestion must be visible for a heavy model below 600 s")
	}
	if strings.Contains(page, `data-frontier-hint="600" hidden`) {
		t.Error("the suggestion is hidden for a heavy model")
	}

	// any whole number is accepted, junk is refused and changes nothing
	br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-mini"}, "timeout": {"900"}})
	if got := a.Admin.Set.HelperTimeout("grok"); got != 900*time.Second {
		t.Fatalf("stored %s", got)
	}
	for _, bad := range []string{"0", "-1", "abc", "1.5", "99999999"} {
		br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"grok-4.7-reasoning"}, "timeout": {bad}})
		if got := a.Admin.Set.HelperTimeout("grok"); got != 900*time.Second {
			t.Fatalf("%q changed the timeout to %s", bad, got)
		}
		if m := a.Admin.Set.Model("grok"); m != "grok-mini" {
			t.Fatalf("%q: the model changed to %q although the form was refused", bad, m)
		}
	}
	_, page = br.get("/admin")
	if !strings.Contains(page, `name="timeout" value="900"`) || strings.Contains(page, `data-frontier-hint="600" >`) {
		t.Error("the field shows the stored value; at 900 s no suggestion is needed")
	}
}

// The stored timeout is the silence limit of a suggestion: a silent model is given up on after exactly that long.
func TestSuggestUsesTheStoredTimeout(t *testing.T) {
	r := newSuggestRig(t, true, true)
	r.mu.Lock()
	r.hang = true
	r.mu.Unlock()
	r.postJSON("/admin/providers/grok/model", url.Values{"model": {"helper-2"}, "timeout": {"1"}})
	if got := r.a.Admin.Set.HelperTimeout("grok"); got != time.Second {
		t.Fatalf("stored %s", got)
	}
	start := time.Now()
	resp, plain := r.postJSON("/admin/upstreams/suggest", url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
	to, _ := plain["timeout"].(map[string]any)
	if resp.StatusCode != 504 || to == nil || to["kind"] != "idle" || to["secs"] != float64(1) || time.Since(start) > 5*time.Second {
		t.Fatalf("%d %v after %s", resp.StatusCode, plain, time.Since(start))
	}
}
