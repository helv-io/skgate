package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseOne(t *testing.T, doc string) ImportItem {
	t.Helper()
	items, err := ParseImport(doc)
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %+v", err, items)
	}
	return items[0]
}

func TestImportClaudeDesktopShape(t *testing.T) {
	items, err := ParseImport(`{"mcpServers": {
	  "files": {"command": "npx", "args": ["-y", "@scope/server-files", "/data"], "env": {"TOKEN": "abc", "N": 5}},
	  "Search Tool": {"command": "uvx", "args": ["pkg"]},
	  "remote": {"url": "https://mcp.example.com/mcp", "headers": {"Authorization": "Bearer tok-123", "X-Key": "k"}}
	}}`)
	if err != nil || len(items) != 3 {
		t.Fatalf("%v %+v", err, items)
	}
	by := map[string]ImportItem{}
	for _, it := range items {
		if it.Err != "" {
			t.Fatalf("%s: %s", it.Name, it.Err)
		}
		by[it.Upstream.Alias] = it
	}
	f := by["files"].Upstream
	if f.Kind != KindStdio || f.Command != "npx" || strings.Join(f.Args, " ") != "-y @scope/server-files /data" ||
		len(f.Env) != 2 || f.Env[0].Name != "N" || f.Env[0].Value != "5" || f.Env[1].Value != "abc" || !f.Enabled {
		t.Fatalf("%+v", f)
	}
	s := by["search-tool"]
	if s.Upstream.Command != "uvx" || len(s.Notes) != 1 || !strings.Contains(s.Notes[0], "sanitized") {
		t.Fatalf("%+v", s)
	}
	r := by["remote"].Upstream
	if r.Kind != KindRemote || r.URL != "https://mcp.example.com/mcp" || r.AuthKind != AuthBearer || r.AuthValue != "tok-123" ||
		len(r.Headers) != 1 || r.Headers[0].Name != "X-Key" {
		t.Fatalf("%+v", r)
	}
}

func TestImportSingleServerAndBareMap(t *testing.T) {
	it := parseOne(t, `{"command": "node", "args": ["server.js"]}`)
	if it.Upstream.Alias != "server" || it.Upstream.Command != "node" {
		t.Fatalf("%+v", it)
	}
	it = parseOne(t, `{"name": "My Server", "url": "http://localhost:8080/mcp"}`)
	if it.Upstream.Alias != "my-server" || it.Upstream.Kind != KindRemote || it.Upstream.AuthKind != AuthAuto {
		t.Fatalf("%+v", it)
	}
	items, err := ParseImport(`{"a": {"command": "x"}, "b": {"url": "https://h/mcp"}}`)
	if err != nil || len(items) != 2 || items[0].Upstream.Alias != "a" {
		t.Fatalf("%v %+v", err, items)
	}
	items, err = ParseImport(`{"servers": {"vs": {"type": "stdio", "command": "x"}}}`)
	if err != nil || len(items) != 1 || items[0].Err != "" {
		t.Fatalf("servers key: %v %+v", err, items)
	}
}

func TestImportTypes(t *testing.T) {
	if it := parseOne(t, `{"a": {"type": "http", "url": "https://h/mcp"}}`); it.Err != "" || it.Upstream.Kind != KindRemote {
		t.Fatalf("%+v", it)
	}
	it := parseOne(t, `{"a": {"type": "sse", "url": "https://h/sse"}}`)
	if it.Err != "" || len(it.Notes) == 0 || !strings.Contains(strings.Join(it.Notes, ";"), "sse") {
		t.Fatalf("%+v", it)
	}
	if it := parseOne(t, `{"a": {"type": "stdio", "command": "x", "disabled": true}}`); it.Upstream.Enabled {
		t.Fatal("disabled flag")
	}
}

func TestImportErrorsReportedPerItem(t *testing.T) {
	items, err := ParseImport(`{"mcpServers": {
	  "ok": {"command": "x"},
	  "both": {"command": "x", "url": "https://h/"},
	  "none": {},
	  "!!!": {"command": "x"},
	  "badtype": {"type": "websocket", "url": "ws://h"},
	  "stdio-no-cmd": {"type": "stdio", "url": "https://h/"},
	  "http-no-url": {"type": "http", "command": "x"},
	  "spaces": {"command": "npx -y pkg"},
	  "badargs": {"command": "x", "args": "a b"},
	  "badargitem": {"command": "x", "args": [["nested"]]},
	  "badenv": {"command": "x", "env": {"1BAD": "v"}},
	  "badenvval": {"command": "x", "env": {"A": {"x": 1}}},
	  "badurl": {"url": "ftp://h/"},
	  "badheaders": {"url": "https://h/", "headers": {"Bad Name": "v"}},
	  "notobj": 5
	}}`)
	if err != nil {
		t.Fatal(err)
	}
	errs := map[string]string{}
	for _, it := range items {
		errs[it.Name] = it.Err
	}
	if errs["ok"] != "" {
		t.Errorf("ok: %s", errs["ok"])
	}
	for _, n := range []string{"both", "none", "!!!", "badtype", "stdio-no-cmd", "http-no-url", "spaces", "badargs", "badargitem", "badenv", "badenvval", "badurl", "badheaders", "notobj"} {
		if errs[n] == "" {
			t.Errorf("%s: expected an error", n)
		}
	}
	if !strings.Contains(errs["spaces"], "shell mode") {
		t.Errorf("hint: %s", errs["spaces"])
	}
}

func TestImportDocumentErrors(t *testing.T) {
	for name, doc := range map[string]string{
		"empty": "", "spaces": "  ", "not json": "{nope", "array": `[{"command":"x"}]`, "trailing": `{"a":{"command":"x"}} {}`,
		"empty servers": `{"mcpServers": {}}`, "servers not object": `{"mcpServers": []}`, "too big": `{"a": "` + strings.Repeat("x", 1<<20) + `"}`,
	} {
		if _, err := ParseImport(doc); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	var sb strings.Builder
	sb.WriteString(`{"mcpServers":{`)
	for i := 0; i < 101; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"s` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":{"command":"x"}`)
	}
	sb.WriteString("}}")
	if _, err := ParseImport(sb.String()); err == nil || !strings.Contains(err.Error(), "100") {
		t.Errorf("limit: %v", err)
	}
}

func TestSanitizeAlias(t *testing.T) {
	for in, want := range map[string]string{"Files": "files", "my_server.v2": "my-server-v2", "  a  b ": "a-b", "--x--": "x", "ÄÖ": "", "a/b": "a-b",
		strings.Repeat("a", 80): strings.Repeat("a", 63), "@scope/pkg": "scope-pkg"} {
		if got := SanitizeAlias(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestStoreImportConflictsAndManagedSwitch(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Upstreams.Create(Upstream{Alias: "taken", URL: "http://h/mcp", AuthKind: AuthNone, Enabled: true})
	items, _ := ParseImport(`{"mcpServers": {"taken": {"command": "x"}, "new": {"command": "y"}, "New!": {"command": "z"}, "bad": {}, "r": {"url": "https://h/mcp", "headers": {"X-A": "1"}}}}`)
	res := e.srv.Upstreams.StoreImport(items, true, true)
	got := map[string]string{}
	for _, r := range res {
		got[r.Name] = r.Status
	}
	if got["taken"] != ImportSkipped || (got["new"] == got["New!"]) || (got["new"] != ImportCreated && got["New!"] != ImportCreated) ||
		got["bad"] != ImportInvalid || got["r"] != ImportCreated {
		t.Fatalf("%+v", res)
	}
	u, ok := e.srv.Upstreams.Get("new")
	if !ok || !u.IncludeInMCP || (u.Command != "y" && u.Command != "z") {
		t.Fatalf("%+v", u)
	}
	// slim edition: managed entries are refused, remote ones still import
	slimEdition(t)
	items, _ = ParseImport(`{"m": {"command": "x"}, "r2": {"url": "https://h/mcp"}}`)
	res = e.srv.Upstreams.StoreImport(items, false, false)
	if res[0].Status != ImportInvalid || !strings.Contains(res[0].Detail, "slim image") || res[1].Status != ImportCreated {
		t.Fatalf("%+v", res)
	}
	if u, _ := e.srv.Upstreams.Get("r2"); u.IncludeInMCP {
		t.Fatal("include flag must follow the form")
	}
}

func TestExportOmitsSecretsAndRoundTrips(t *testing.T) {
	ups := []Upstream{
		{Alias: "files", Kind: KindStdio, Command: "npx", Args: []string{"-y", "pkg"}, Env: []KV{{"TOKEN", "topsecret-value"}}, Install: "npm ci",
			Lifecycle: "always", IdleSecs: 30, Enabled: true, IncludeInMCP: true},
		{Alias: "remote", Kind: KindRemote, URL: "https://h/mcp", AuthKind: AuthBearer, AuthValue: "bearer-secret-123",
			Headers: []KV{{"X-Key", "header-secret-456"}}, HostOverride: "api.internal", Enabled: false},
		{Alias: "repo", Kind: KindGit, Command: "python", Args: []string{"-m", "srv"}, GitURL: "https://h/r.git", GitRef: "v1", GitToken: "git-secret-789", Enabled: true},
	}
	out := string(ExportJSON(ups))
	for _, secret := range []string{"topsecret-value", "bearer-secret-123", "header-secret-456", "git-secret-789"} {
		if strings.Contains(out, secret) {
			t.Fatalf("export leaks %s:\n%s", secret, out)
		}
	}
	var doc struct {
		McpServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.McpServers) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
	items, err := ParseImport(out)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Upstream{}
	for _, it := range items {
		if it.Err != "" {
			t.Fatalf("%s: %s", it.Name, it.Err)
		}
		by[it.Upstream.Alias] = it.Upstream
	}
	f := by["files"]
	if f.Command != "npx" || f.Lifecycle != "always" || f.Install != "npm ci" || f.IdleSecs != 30 || len(f.Env) != 1 || f.Env[0].Value != "" || !f.IncludeInMCP {
		t.Fatalf("%+v", f)
	}
	if g := by["repo"]; g.Kind != KindGit || g.GitURL != "https://h/r.git" || g.GitRef != "v1" {
		t.Fatalf("%+v", g)
	}
	if r := by["remote"]; r.Enabled || r.HostOverride != "api.internal" || len(r.Headers) != 0 || r.AuthKind != AuthAuto {
		t.Fatalf("%+v", r)
	}
}

func TestImportIgnoresCwd(t *testing.T) {
	it := parseOne(t, `{"a": {"command": "x", "cwd": "/somewhere"}}`)
	if it.Err != "" || it.Upstream.WorkDir != "" || !strings.Contains(strings.Join(it.Notes, ";"), "cwd ignored") {
		t.Fatalf("%+v", it)
	}
}
