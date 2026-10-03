package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helv-io/skgate/internal/config"
	"github.com/helv-io/skgate/internal/oidctest"
	"github.com/helv-io/skgate/internal/provider/grok"
	"github.com/helv-io/skgate/internal/suggest"
)

const tokenSecret = "glpat-VERYSECRETTOKEN-9876"

// suggestRig is an admin session whose provider is a mock model endpoint with structured output, and
// whose fetcher reads from a mock registry and forge.
type suggestRig struct {
	br     *browser
	csrf   string
	a      *App
	mu     sync.Mutex
	prompt []string // every body sent to the model
	reply  string
	hang   bool // the model never answers (until the request ends)
}

func newSuggestRig(t *testing.T, signIn, pickModel bool) *suggestRig {
	r := &suggestRig{reply: `{"alias":"mcp-thing","transport":"stdio","command":"node","args":["dist/index.js"],"env":[{"name":"THING_KEY","description":"key","secret":true,"required":true}],"headers":[],"install":"npm ci && npm run build","startup_secs":90,"notes":["n"],"warnings":["w"],"confidence":"high"}`}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		switch req.URL.Path {
		case "/v1/models":
			io.WriteString(w, `{"object":"list","data":[{"id":"helper-1"},{"id":"helper-2"}]}`)
		case "/v1/chat/completions":
			r.mu.Lock()
			r.prompt = append(r.prompt, string(b))
			reply, hang := r.reply, r.hang
			r.mu.Unlock()
			if hang {
				<-req.Context().Done()
				return
			}
			c, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": reply}}}})
			w.Write(c)
		}
	}))
	t.Cleanup(model.Close)
	forge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.Contains(req.URL.Path, "/projects/") && !strings.Contains(req.URL.Path, "/repos/") {
			w.WriteHeader(404)
			return
		}
		if req.Header.Get("Private-Token") != tokenSecret && req.Header.Get("Authorization") != "Bearer "+tokenSecret {
			w.WriteHeader(404)
			return
		}
		if strings.Contains(req.URL.Path, "README.md") {
			io.WriteString(w, "# Thing\nRequires THING_KEY. Build with npm.")
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(forge.Close)
	a, _, br, csrf := signedIn(t, func(c *config.Config, _ *oidctest.Provider) {
		c.ManagedDir = t.TempDir()
	})
	t.Cleanup(a.MCP.ShutdownManaged)
	_ = a.DB.SetSetting("provider.grok.base", model.URL+"/v1")
	_ = a.DB.SetSetting("provider.grok.fallback", "")
	r.a, r.br, r.csrf = a, br, csrf
	if signIn {
		a.Providers.Default().(*grok.Client).SetTokens("acc", "ref", time.Now().Add(time.Hour))
	}
	if pickModel {
		br.post("/admin/providers/grok/models/reload", url.Values{"csrf": {csrf}})
		br.post("/admin/providers/grok/model", url.Values{"csrf": {csrf}, "model": {"helper-2"}})
	}
	f := suggest.NewFetcher()
	f.NPM, f.PyPI = forge.URL+"/npm", forge.URL+"/pypi"
	f.GitHubAPI = forge.URL + "/repos-github"
	a.Admin.SuggestFetch = f
	// GitLab and Gitea hosts are reached over https; point their API calls at the mock
	f.Client = &http.Client{Transport: redirectTo(forge.URL), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return r
}

type redirectTo string

func (r redirectTo) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(string(r))
	c := req.Clone(req.Context())
	c.URL.Scheme, c.URL.Host = u.Scheme, u.Host
	if !strings.HasPrefix(c.URL.Path, "/npm") && !strings.HasPrefix(c.URL.Path, "/pypi") && !strings.HasPrefix(c.URL.Path, "/repos-github") && req.URL.Host != u.Host {
		c.URL.Path = "/projects/x" + c.URL.Path // any forge path
	}
	return http.DefaultTransport.RoundTrip(c)
}

func (r *suggestRig) suggest(v url.Values) (int, map[string]any) {
	v.Set("csrf", r.csrf)
	resp, body := r.br.post("/admin/upstreams/suggest", v)
	var m map[string]any
	json.Unmarshal([]byte(body), &m)
	return resp.StatusCode, m
}

func TestSuggestEndpointFillsResultAndSavesNothing(t *testing.T) {
	r := newSuggestRig(t, true, true)
	st, m := r.suggest(url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
	if st != 200 || m["command"] != "node" || m["kind"] != "git" || m["git_url"] != "https://gitlab.com/grp/thing" || m["install"] != "npm ci && npm run build" || m["confidence"] != "high" {
		t.Fatalf("%d %v", st, m)
	}
	env := m["env"].([]any)[0].(map[string]any)
	if env["name"] != "THING_KEY" || env["secret"] != true || env["required"] != true || env["value"] != nil {
		t.Fatalf("env %v", env)
	}
	if list, _ := r.a.MCP.Upstreams.List(); len(list) != 0 {
		t.Fatalf("nothing may be saved, got %d upstreams", len(list))
	}
	// the model saw the README and the chosen model, never the token
	if len(r.prompt) != 1 || !strings.Contains(r.prompt[0], "THING_KEY") || !strings.Contains(r.prompt[0], `"model":"helper-2"`) {
		t.Fatalf("prompt: %v", r.prompt)
	}
	for _, p := range r.prompt {
		if strings.Contains(p, tokenSecret) {
			t.Fatal("token sent to the model")
		}
	}
}

func TestSuggestEndpointErrorsAreClearAndLeakNothing(t *testing.T) {
	r := newSuggestRig(t, true, true)
	// invalid model output is rejected
	r.reply = `{"alias":"x","transport":"stdio","command":"rm","args":["-rf","/"],"env":[],"headers":[],"install":"","startup_secs":30,"notes":[],"warnings":[],"confidence":"high"}`
	st, m := r.suggest(url.Values{"source": {"https://gitlab.com/grp/thing"}, "git_token": {tokenSecret}})
	if st != 422 || !strings.Contains(m["error"].(string), "unusable") || strings.Contains(m["error"].(string), tokenSecret) {
		t.Fatalf("%d %v", st, m)
	}
	// a private repository without token
	st, m = r.suggest(url.Values{"source": {"https://gitlab.com/grp/thing"}})
	if st != 422 || !strings.Contains(m["error"].(string), "token") {
		t.Fatalf("%d %v", st, m)
	}
	// unsupported ecosystems say what is missing
	st, m = r.suggest(url.Values{"source": {"cargo:rmcp"}})
	if st != 422 || !strings.Contains(m["error"].(string), "cargo") || !strings.Contains(m["error"].(string), "available runners") {
		t.Fatalf("%d %v", st, m)
	}
	if st, _ = r.suggest(url.Values{"source": {"not a source"}}); st != 400 {
		t.Fatalf("bad source: %d", st)
	}
}

// The helper is optional: without an account or a model it reports why, while the manual form saves
// upstreams exactly as before.
func TestManualFormNeverGated(t *testing.T) {
	for name, rig := range map[string]*suggestRig{"signed out": newSuggestRig(t, false, false), "no model": newSuggestRig(t, true, false)} {
		st, m := rig.suggest(url.Values{"source": {"mcp-thing"}})
		if st != 409 || m["error"] == "" {
			t.Fatalf("%s: %d %v", name, st, m)
		}
		_, page := rig.br.get("/admin/upstreams")
		if !strings.Contains(page, `data-suggest="/admin/upstreams/suggest" disabled title="`) {
			t.Errorf("%s: the helper button must be disabled with a tooltip", name)
		}
		for _, want := range []string{`name="command_pick"`, `name="args"`, `name="env_name"`, `name="install"`, `name="startup_secs"`, `name="source"`, `name="git_token"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: manual field %s missing", name, want)
			}
		}
		form := stdioForm(rig.csrf, "manual", url.Values{})
		resp, _ := rig.br.post("/admin/upstreams/save", form)
		if flashKind(resp) != "ok" {
			k, msg := flashOf(resp)
			t.Fatalf("%s: manual save failed: %s %s", name, k, msg)
		}
		if u, ok := rig.a.MCP.Upstreams.Get("manual"); !ok || u.Command != os.Args[0] {
			t.Fatalf("%s: upstream not saved", name)
		}
	}
	// enabled when signed in with a model
	rig := newSuggestRig(t, true, true)
	_, page := rig.br.get("/admin/upstreams")
	if strings.Contains(page, `data-suggest="/admin/upstreams/suggest" disabled`) {
		t.Fatal("helper should be enabled")
	}
}

func TestSuggestEndpointIsPostAndCSRFOnly(t *testing.T) {
	r := newSuggestRig(t, true, true)
	if resp, _ := r.br.get("/admin/upstreams/suggest"); resp.StatusCode != 405 {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	resp, _ := r.br.post("/admin/upstreams/suggest", url.Values{"source": {"mcp-thing"}})
	if resp.StatusCode != 403 {
		t.Fatalf("no csrf: %d", resp.StatusCode)
	}
}

// The single managed form: a repository in the source field makes a git upstream, a command stays a
// command upstream, and existing records keep working.
func TestSingleManagedFormDerivesKind(t *testing.T) {
	r := newSuggestRig(t, false, false)
	ok := stdioForm(r.csrf, "hosted", url.Values{"source": {"https://git.example.com/org/repo/tree/dev"}, "git_token": {tokenSecret}, "git_ref": {""}})
	if resp, _ := r.br.post("/admin/upstreams/save", ok); flashKind(resp) != "ok" {
		k, m := flashOf(resp)
		t.Fatalf("%s %s", k, m)
	}
	u, _ := r.a.MCP.Upstreams.Get("hosted")
	if u.Kind != "git" || u.GitURL != "https://git.example.com/org/repo" || u.GitRef != "dev" || u.GitToken != tokenSecret {
		t.Fatalf("%+v", u)
	}
	_, page := r.br.get("/admin/upstreams/hosted/edit")
	if strings.Contains(page, tokenSecret) || !strings.Contains(page, `value="https://git.example.com/org/repo"`) {
		t.Fatal("edit form must show the source and only a masked token")
	}
	// a command upstream is unchanged
	if resp, _ := r.br.post("/admin/upstreams/save", stdioForm(r.csrf, "cmd", nil)); flashKind(resp) != "ok" {
		t.Fatal("command upstream")
	}
	if c, _ := r.a.MCP.Upstreams.Get("cmd"); c.Kind != "stdio" {
		t.Fatalf("%+v", c)
	}
	// unsupported source
	bad := stdioForm(r.csrf, "rust", url.Values{"source": {"cargo:rmcp"}})
	if resp, _ := r.br.post("/admin/upstreams/save", bad); flashKind(resp) != "bad" {
		t.Fatal("unsupported source must be refused")
	}
}
