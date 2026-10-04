package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// specHost serves a description that a test can change between requests.
type specHost struct {
	*httptest.Server
	mu   sync.Mutex
	spec string
}

func newSpecHost(t *testing.T) *specHost {
	h := &specHost{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/spec.json" {
			h.mu.Lock()
			defer h.mu.Unlock()
			fmt.Fprint(w, h.spec)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	h.set(fmt.Sprintf(oaAdminSpec, h.URL))
	t.Cleanup(h.Close)
	return h
}

func (h *specHost) set(s string) { h.mu.Lock(); h.spec = s; h.mu.Unlock() }

// v2 is the description with one operation changed, one added and one removed.
func (h *specHost) v2(t *testing.T) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(fmt.Sprintf(oaAdminSpec, h.URL)), &m); err != nil {
		t.Fatal(err)
	}
	paths := m["paths"].(map[string]any)
	delete(paths, "/files")
	paths["/tags"] = map[string]any{"get": map[string]any{"operationId": "listTags"}}
	paths["/notes"].(map[string]any)["get"].(map[string]any)["summary"] = "List all notes"
	b, _ := json.Marshal(m)
	return string(b)
}

func TestOpenAPIUpdateIsManualReviewedAndCancellable(t *testing.T) {
	a, _, br, csrf := signedIn(t, nil)
	host := newSpecHost(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"notes"}, "enabled": {"1"}, "oa_spec_url": {host.URL + "/spec.json"}})
	st, err := a.MCP.Upstreams.OpenAPI("notes")
	if err != nil || st.Config.FetchedAt == 0 || len(st.Config.SpecHash) != 64 {
		t.Fatalf("an upstream added from an address records when and what it read: %+v %v", st.Config, err)
	}
	stored := st.Config.Spec
	_, page := br.get("/admin/upstreams/notes/tools")
	if !strings.Contains(page, `action="/admin/upstreams/notes/spec/update"`) || !strings.Contains(page, ">Update</button>") || !strings.Contains(page, "Updated ") {
		t.Fatalf("an upstream with an address offers Update and says when it was read:\n%s", page)
	}

	// the same text at the address: nothing to review
	r, _ := br.post("/admin/upstreams/notes/spec/update", url.Values{"csrf": {csrf}})
	if _, m := flashOf(r); !strings.Contains(m, "Unchanged") || r.Header.Get("Location") != "/admin/upstreams/notes/tools" {
		t.Fatalf("unchanged: %q %s", m, r.Header.Get("Location"))
	}

	// a changed description is shown for review and stores nothing
	host.set(host.v2(t))
	r, review := br.post("/admin/upstreams/notes/spec/update", url.Values{"csrf": {csrf}})
	token := tokenOf(t, review)
	if r.StatusCode != 200 {
		k, m := flashOf(r)
		t.Fatalf("update: %d %s %s", r.StatusCode, k, m)
	}
	for _, want := range []string{"Updates run through an SI layer before they are reused", "SI layer", "GET /tags", "POST /files", "GET /notes", "Confirm update", ">Cancel</button>", "Tools exposed", "Kept for every tool that still exists"} {
		if !strings.Contains(review, want) {
			t.Errorf("review lacks %q", want)
		}
	}
	for _, bad := range []string{"AI layer", "Grok", "grok-"} {
		if strings.Contains(review, bad) {
			t.Errorf("review must not say %q", bad)
		}
	}
	if now, _ := a.MCP.Upstreams.OpenAPI("notes"); now.Config.Spec != stored {
		t.Fatal("review must store nothing")
	}

	// cancel leaves everything untouched, and the token is spent
	r, _ = br.post("/admin/upstreams/notes/spec/cancel", url.Values{"csrf": {csrf}, "token": {token}})
	if _, m := flashOf(r); !strings.Contains(m, "cancelled") {
		t.Errorf("cancel: %q", m)
	}
	if now, _ := a.MCP.Upstreams.OpenAPI("notes"); now.Config.Spec != stored {
		t.Fatal("cancel must change nothing")
	}
	r, _ = br.post("/admin/upstreams/notes/spec/apply", url.Values{"csrf": {csrf}, "token": {token}})
	if _, e := flashOf(r); !strings.Contains(e, "expired") {
		t.Errorf("a cancelled update cannot be applied: %q", e)
	}
	if now, _ := a.MCP.Upstreams.OpenAPI("notes"); now.Config.Spec != stored {
		t.Fatal("a cancelled update must not apply")
	}

	// confirm replaces the description and keeps the tool selection where the tools still exist
	_, review = br.post("/admin/upstreams/notes/spec/update", url.Values{"csrf": {csrf}})
	token = tokenOf(t, review)
	r, _ = br.post("/admin/upstreams/notes/spec/apply", url.Values{"csrf": {csrf}, "token": {token}})
	if _, m := flashOf(r); !strings.Contains(m, "notes updated") {
		t.Fatalf("apply: %q", m)
	}
	after, _ := a.MCP.Upstreams.OpenAPI("notes")
	if !strings.Contains(after.Config.Spec, "/tags") || strings.Contains(after.Config.Spec, "/files") {
		t.Fatal("the new description is stored")
	}
	if !after.Config.Selection.Enabled["GET /notes"] || after.Config.Selection.Enabled["GET /tags"] {
		t.Fatalf("selection kept for existing tools, new ones off: %v", after.Config.Selection.Enabled)
	}
	if a.MCP.Upstreams.OpenAPIToolCount("notes") != 2 {
		t.Errorf("tools after: %d", a.MCP.Upstreams.OpenAPIToolCount("notes"))
	}
	// the token works once
	r, _ = br.post("/admin/upstreams/notes/spec/apply", url.Values{"csrf": {csrf}, "token": {token}})
	if _, e := flashOf(r); !strings.Contains(e, "expired") {
		t.Errorf("a spent update cannot be applied again: %q", e)
	}
}

func TestOpenAPIPastedDefinitionsCannotBeUpdated(t *testing.T) {
	_, _, br, csrf := signedIn(t, nil)
	api := oaAPI(t)
	br.post("/admin/upstreams/save", url.Values{"csrf": {csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"pasted"}, "enabled": {"1"},
		"oa_spec_text": {fmt.Sprintf(oaAdminSpec, api.URL)}})
	_, page := br.get("/admin/upstreams/pasted/tools")
	if !strings.Contains(page, `disabled aria-describedby="oa-update-why" title="pasted definitions can&#39;t be updated"`) || !strings.Contains(page, `id="oa-update-why">pasted definitions can&#39;t be updated</span>`) {
		t.Errorf("a pasted description gets a disabled Update with the reason as tooltip and as visible text:\n%s", page)
	}
	if strings.Contains(page, "/spec/update") {
		t.Error("no update form for a pasted description")
	}
	r, _ := br.post("/admin/upstreams/pasted/spec/update", url.Values{"csrf": {csrf}})
	if _, e := flashOf(r); !strings.Contains(e, "pasted definitions can't be updated") {
		t.Errorf("posting anyway is refused: %q", e)
	}
}

var reviewTokenRe = regexp.MustCompile(`name="token" value="([0-9a-f]+)"`)

func tokenOf(t *testing.T, page string) string {
	m := reviewTokenRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no review token on the page:\n%.600s", page)
	}
	return m[1]
}

const updV1 = `{"openapi":"3.0.0","info":{"title":"T","version":"1"},"servers":[{"url":"http://x.example"}],"paths":{"/a":{"get":{"operationId":"getA"}}}}`
const updV2 = `{"openapi":"3.0.0","info":{"title":"T","version":"2"},"servers":[{"url":"http://x.example"}],"paths":{"/a":{"get":{"operationId":"getA"}},"/b":{"get":{"summary":"b"}}}}`

// The update runs the new description through the SI layer (the same repair as Check description): the problems it
// fixes are counted on the review screen. Without a helper model the layer is skipped and says why.
func TestOpenAPIUpdateRunsTheSILayer(t *testing.T) {
	for _, withHelper := range []bool{true, false} {
		r := newSuggestRig(t, withHelper, withHelper)
		r.a.Admin.NoSaveTest = true
		var mu sync.Mutex
		spec := updV1
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			fmt.Fprint(w, spec)
		}))
		defer srv.Close()
		r.br.post("/admin/upstreams/save", url.Values{"csrf": {r.csrf}, "mode": {"new"}, "kind": {"openapi"}, "alias": {"t"}, "enabled": {"1"}, "oa_spec_url": {srv.URL}})
		mu.Lock()
		spec = updV2
		mu.Unlock()
		r.reply = `{"patches":[{"op":"set","path":"/paths/~1b/get/operationId","value":"getB","reason":"missing id"}]}`
		_, review := r.br.post("/admin/upstreams/t/spec/update", url.Values{"csrf": {r.csrf}})
		if !strings.Contains(review, "Confirm update") || !strings.Contains(review, "GET /b") {
			t.Fatalf("helper=%v: no review:\n%.800s", withHelper, review)
		}
		if withHelper && !strings.Contains(review, "Fixed 1 problem; 0 problems left.") {
			t.Errorf("the SI layer fixed the missing id: %.1500s", review)
		}
		if !withHelper && !strings.Contains(review, "Not run, so 1 problem stay") {
			t.Errorf("without a helper the layer is skipped and says so: %.1500s", review)
		}
		if strings.Contains(review, "AI layer") {
			t.Error("it is the SI layer")
		}
		r.br.post("/admin/upstreams/t/spec/apply", url.Values{"csrf": {r.csrf}, "token": {tokenOf(t, review)}})
		st, _ := r.a.MCP.Upstreams.OpenAPI("t")
		if withHelper != strings.Contains(st.Config.Spec, `"getB"`) {
			t.Errorf("helper=%v: stored spec %s", withHelper, st.Config.Spec)
		}
	}
}
