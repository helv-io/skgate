package mcp

import (
	"strings"
	"testing"

	"github.com/helv-io/skgate/internal/secrets"
)

func rawCol(t *testing.T, e *env, alias, col string) string {
	t.Helper()
	var v string
	if err := e.db.QueryRow(`SELECT `+col+` FROM upstreams WHERE alias=?`, alias).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestManagedUpstreamRoundTripAndSealing(t *testing.T) {
	e := newEnv(t, nil)
	u := Upstream{Alias: "tools", Kind: KindStdio, Command: "npx", Args: []string{"-y", "@scope/pkg", "--flag=a b"},
		Env: []KV{{"API_TOKEN", "super-secret-value"}, {"MODE", "x"}}, Install: "npm ci", Lifecycle: "always",
		StartupSecs: 90, IdleSecs: 120, Enabled: true, IncludeInMCP: true}
	if err := e.srv.Upstreams.Create(u); err != nil {
		t.Fatal(err)
	}
	got, ok := e.srv.Upstreams.Get("tools")
	if !ok || !got.Managed() || got.Command != "npx" || len(got.Args) != 3 || got.Args[2] != "--flag=a b" ||
		len(got.Env) != 2 || got.Env[0].Value != "super-secret-value" || got.Lifecycle != "always" || got.StartupSecs != 90 || got.IdleSecs != 120 || !got.IncludeInMCP {
		t.Fatalf("%+v", got)
	}
	if raw := rawCol(t, e, "tools", "env"); !secrets.IsSealed(raw) || strings.Contains(raw, "super-secret-value") || strings.Contains(raw, "API_TOKEN") {
		t.Fatalf("env not sealed at rest: %q", raw)
	}
	if got.AuthKind != AuthNone || got.URL != "" || got.SecretErr {
		t.Fatalf("%+v", got)
	}
	// update keeping the secret values by name when the form leaves them empty
	edit := got
	edit.Env = []KV{{"API_TOKEN", ""}, {"MODE", "y"}, {"NEW", "n"}}
	edit = MergeSecrets(got, edit, false)
	if err := e.srv.Upstreams.Update(edit, false); err != nil {
		t.Fatal(err)
	}
	got, _ = e.srv.Upstreams.Get("tools")
	if len(got.Env) != 3 || got.Env[0].Value != "super-secret-value" || got.Env[1].Value != "y" {
		t.Fatalf("%+v", got.Env)
	}
}

func TestRemoteCustomHeadersSealedAndApplied(t *testing.T) {
	e := newEnv(t, nil)
	u := Upstream{Alias: "r", URL: "http://example.invalid/mcp", AuthKind: AuthBearer, AuthValue: "tok",
		Headers: []KV{{"X-Api-Key", "abcd-1234-efgh"}, {"X-Tenant", "t1"}}, Enabled: true}
	if err := e.srv.Upstreams.Create(u); err != nil {
		t.Fatal(err)
	}
	if raw := rawCol(t, e, "r", "headers"); !secrets.IsSealed(raw) || strings.Contains(raw, "abcd-1234") {
		t.Fatalf("headers not sealed: %q", raw)
	}
	got, _ := e.srv.Upstreams.Get("r")
	if len(got.Headers) != 2 || got.Headers[0].Value != "abcd-1234-efgh" || got.Kind != KindRemote {
		t.Fatalf("%+v", got)
	}
	if fingerprint(got) == fingerprint(Upstream{URL: got.URL, AuthKind: AuthBearer, AuthValue: "tok"}) {
		t.Fatal("headers must change the session fingerprint")
	}
}

func TestExistingRemoteUpstreamsUnchanged(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.srv.Upstreams.Create(Upstream{Alias: "old", URL: "http://x/mcp", AuthKind: AuthNone, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.srv.Upstreams.Get("old")
	if got.Kind != KindRemote || got.Managed() || got.Lifecycle != "on-demand" || len(got.Headers) != 0 || len(got.Env) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestUpstreamValidationByKind(t *testing.T) {
	ok := Upstream{Alias: "a", Kind: KindStdio, Command: "uvx", Args: []string{"pkg"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Upstream{
		"empty command":           {Alias: "a", Kind: KindStdio, Command: " "},
		"managed with url":        {Alias: "a", Kind: KindStdio, Command: "x", URL: "http://h/"},
		"managed with bearer":     {Alias: "a", Kind: KindStdio, Command: "x", AuthKind: AuthBearer, AuthValue: "t"},
		"managed with host":       {Alias: "a", Kind: KindStdio, Command: "x", HostOverride: "h"},
		"managed with headers":    {Alias: "a", Kind: KindStdio, Command: "x", Headers: []KV{{"X-A", "b"}}},
		"stdio with git":          {Alias: "a", Kind: KindStdio, Command: "x", GitURL: "https://h/x.git"},
		"git without url":         {Alias: "a", Kind: KindGit, Command: "x"},
		"git with workdir":        {Alias: "a", Kind: KindGit, Command: "x", GitURL: "https://h/x.git", WorkDir: "/w"},
		"bad env name":            {Alias: "a", Kind: KindStdio, Command: "x", Env: []KV{{"1A", "b"}}},
		"bad alias":               {Alias: "A_b", Kind: KindStdio, Command: "x"},
		"negative timeout":        {Alias: "a", Kind: KindStdio, Command: "x", IdleSecs: -1},
		"bad lifecycle":           {Alias: "a", Kind: KindStdio, Command: "x", Lifecycle: "weekly"},
		"remote with command":     {Alias: "a", URL: "http://h/", Command: "x"},
		"remote with env":         {Alias: "a", URL: "http://h/", Env: []KV{{"A", "b"}}},
		"remote bad header":       {Alias: "a", URL: "http://h/", Headers: []KV{{"Bad Name", "v"}}},
		"remote reserved header":  {Alias: "a", URL: "http://h/", Headers: []KV{{"Host", "v"}}},
		"remote header newline":   {Alias: "a", URL: "http://h/", Headers: []KV{{"X-A", "v\r\nX-B: c"}}},
		"remote duplicate header": {Alias: "a", URL: "http://h/", Headers: []KV{{"X-A", "1"}, {"x-a", "2"}}},
		"unknown kind":            {Alias: "a", Kind: "docker", Command: "x"},
	}
	for name, u := range cases {
		u := u
		if err := u.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestMergeSecretsKeepsOnlyForSameKind(t *testing.T) {
	old := Upstream{Kind: KindStdio, Env: []KV{{"A", "secret"}}, GitToken: "tok"}
	cur := MergeSecrets(old, Upstream{Kind: KindStdio, Env: []KV{{"A", ""}, {"B", ""}}}, false)
	if cur.Env[0].Value != "secret" || cur.Env[1].Value != "" || cur.GitToken != "tok" {
		t.Fatalf("%+v", cur)
	}
	if cur = MergeSecrets(old, Upstream{Kind: KindStdio}, true); cur.GitToken != "" {
		t.Fatal("clear token")
	}
	if cur = MergeSecrets(old, Upstream{Kind: KindRemote, URL: "http://x"}, false); cur.GitToken != "" || cur.Env != nil {
		t.Fatalf("nothing may carry over to another kind: %+v", cur)
	}
}

func TestUndecryptableManagedSecretsAreFlagged(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Upstreams.Create(Upstream{Alias: "m", Kind: KindStdio, Command: "x", Env: []KV{{"A", "value-1234"}}, Enabled: true})
	e.db.Exec(`UPDATE upstreams SET env='enc:v1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' WHERE alias='m'`)
	got, _ := e.srv.Upstreams.Get("m")
	if !got.SecretErr || len(got.Env) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestMergeSecretsKeepsHiddenSettings(t *testing.T) {
	old := Upstream{Kind: KindStdio, WorkDir: "/srv/custom", IdleSecs: 90}
	if got := MergeSecrets(old, Upstream{Kind: KindStdio}, false); got.WorkDir != "/srv/custom" || got.IdleSecs != 90 {
		t.Fatalf("%q %d", got.WorkDir, got.IdleSecs)
	}
}

// Rows left empty are not stored and not passed to the process, so nothing has to be deleted from the form.
func TestEmptyEnvValuesAreNotStoredOrPassed(t *testing.T) {
	e := newEnv(t, nil)
	u := Upstream{Alias: "tools", Kind: KindStdio, Command: "npx", Lifecycle: "always", Enabled: true,
		Env: []KV{{"API_BASE", "http://x"}, {"OPTIONAL_ONE", ""}, {"OPTIONAL_TWO", ""}}}
	if err := e.srv.Upstreams.Create(u); err != nil {
		t.Fatal(err)
	}
	got, _ := e.srv.Upstreams.Get("tools")
	if len(got.Env) != 1 || got.Env[0].Name != "API_BASE" {
		t.Fatalf("stored: %+v", got.Env)
	}
	// an edit that blanks a row that has a stored value keeps the stored value (the masked-form rule);
	// a new blank row is dropped
	edit := got
	edit.Env = []KV{{"API_BASE", ""}, {"ANOTHER", ""}}
	edit = MergeSecrets(got, edit, false)
	if err := e.srv.Upstreams.Update(edit, false); err != nil {
		t.Fatal(err)
	}
	got, _ = e.srv.Upstreams.Get("tools")
	if len(got.Env) != 1 || got.Env[0].Value != "http://x" {
		t.Fatalf("after edit: %+v", got.Env)
	}
	// rows stored empty by an earlier version never reach the process
	legacy := Upstream{Kind: KindStdio, Env: []KV{{"A", ""}, {"B", "b"}}}
	if sp := legacy.Spec(); len(sp.Env) != 1 || sp.Env[0].Name != "B" {
		t.Fatalf("spec env: %+v", sp.Env)
	}
}
