package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/helv-io/skgate/internal/managed"
)

// ImportItem is one server parsed from pasted JSON, not yet stored.
type ImportItem struct {
	Name     string   // key in the pasted JSON
	Upstream Upstream // Alias is the sanitized name
	Notes    []string // what was changed or ignored
	Err      string   // set when the entry cannot be imported
	// IncludeSet is true when the document said whether to include the server in /mcp.
	IncludeSet bool
}

// ImportResult is the outcome of storing one item.
type ImportResult struct {
	Name, Alias string
	Status      string // created, skipped or invalid
	Detail      string
	Notes       []string
}

// Import result statuses.
const (
	ImportCreated = "created"
	ImportSkipped = "skipped"
	ImportInvalid = "invalid"
)

const maxImportBytes = 1 << 20

// SanitizeAlias turns a free-form server name into a valid alias (a-z, 0-9, dash, at most 63
// characters). It returns "" when nothing usable is left.
func SanitizeAlias(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

// rawServer is the union of the fields used by common MCP client configuration files.
type rawServer struct {
	Type      string          `json:"type"`
	Command   *string         `json:"command"`
	Args      json.RawMessage `json:"args"`
	Env       json.RawMessage `json:"env"`
	Cwd       string          `json:"cwd"`
	URL       string          `json:"url"`
	ServerURL string          `json:"serverUrl"`
	Headers   json.RawMessage `json:"headers"`
	Disabled  bool            `json:"disabled"`
	Skgate    *skgateExt      `json:"skgate"`
}

// skgateExt carries the settings that have no equivalent in other clients' files. Export writes it
// and import reads it, so an export can be imported again.
type skgateExt struct {
	Lifecycle      string `json:"lifecycle,omitempty"`
	Shell          bool   `json:"shell,omitempty"`
	Install        string `json:"install,omitempty"`
	StartupTimeout int    `json:"startupTimeoutSeconds,omitempty"`
	IdleTimeout    int    `json:"idleTimeoutSeconds,omitempty"`
	AutoUpdate     int    `json:"autoUpdateSeconds,omitempty"`
	GitURL         string `json:"gitUrl,omitempty"`
	GitRef         string `json:"gitRef,omitempty"`
	HostOverride   string `json:"hostOverride,omitempty"`
	Aggregate      *bool  `json:"includeInAggregate,omitempty"`
}

func scalarString(raw json.RawMessage) (string, bool) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

func stringMap(raw json.RawMessage, what string) ([]KV, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s must be an object of name: value", what)
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []KV
	for _, k := range names {
		v, ok := scalarString(m[k])
		if !ok {
			return nil, fmt.Errorf("%s %q must be a string", what, k)
		}
		out = append(out, KV{Name: k, Value: v})
	}
	return out, nil
}

// looksLikeServer tells a server object from a container of servers.
func looksLikeServer(m map[string]json.RawMessage) bool {
	for _, k := range []string{"command", "url", "serverUrl"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// ParseImport reads pasted JSON in any of these shapes:
//
//	{"mcpServers": {"name": {...}}}        (also "servers")
//	{"name": {...}, "other": {...}}        (a bare map of servers)
//	{"command": "...", "args": [...]}      (one server; "name" or the fallback name is used)
//
// Entries with a command become stdio upstreams, entries with a url become remote upstreams.
// Problems are reported per item; only unusable documents return an error.
func ParseImport(text string) ([]ImportItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("paste a JSON document")
	}
	if len(text) > maxImportBytes {
		return nil, errors.New("the document is too large (1 MB max)")
	}
	dec := json.NewDecoder(strings.NewReader(text))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return nil, fmt.Errorf("not a JSON object: %s", cleanJSONErr(err))
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the JSON object")
	}
	servers := map[string]json.RawMessage{}
	switch {
	case top["mcpServers"] != nil || top["servers"] != nil:
		key := "mcpServers"
		if top[key] == nil {
			key = "servers"
		}
		if err := json.Unmarshal(top[key], &servers); err != nil {
			return nil, fmt.Errorf("%q must be an object of servers", key)
		}
	case looksLikeServer(top):
		name := "server"
		if n, ok := top["name"]; ok {
			var s string
			if json.Unmarshal(n, &s) == nil && s != "" {
				name = s
			}
		}
		servers[name] = mustRaw(top)
	default:
		for k, v := range top {
			servers[k] = v
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("no servers found")
	}
	if len(servers) > 100 {
		return nil, errors.New("at most 100 servers per import")
	}
	names := make([]string, 0, len(servers))
	for k := range servers {
		names = append(names, k)
	}
	sort.Strings(names)
	var items []ImportItem
	for _, name := range names {
		items = append(items, parseServer(name, servers[name]))
	}
	return items, nil
}

func mustRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func cleanJSONErr(err error) string {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Sprintf("syntax error at byte %d", se.Offset)
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return "expected " + te.Type.String() + " for " + te.Field
	}
	return "invalid JSON"
}

func parseServer(name string, raw json.RawMessage) (it ImportItem) {
	it.Name = name
	alias := SanitizeAlias(name)
	fail := func(f string, a ...any) ImportItem { it.Err = fmt.Sprintf(f, a...); return it }
	if alias == "" {
		return fail("the name has no usable characters (a-z, 0-9)")
	}
	if alias != name {
		it.Notes = append(it.Notes, "name sanitized to "+alias)
	}
	var rs rawServer
	if err := json.Unmarshal(raw, &rs); err != nil {
		return fail("not a valid server object: %s", cleanJSONErr(err))
	}
	u := Upstream{Alias: alias, Enabled: !rs.Disabled, Lifecycle: managed.OnDemand}
	it.Upstream = u
	url := rs.URL
	if url == "" {
		url = rs.ServerURL
	}
	typ := strings.ToLower(strings.TrimSpace(rs.Type))
	switch typ {
	case "", "stdio", "http", "sse", "streamable-http", "streamablehttp":
	default:
		return fail("unsupported type %q (use stdio, http or sse)", rs.Type)
	}
	hasCmd := rs.Command != nil && strings.TrimSpace(*rs.Command) != ""
	switch {
	case hasCmd && url != "":
		return fail("has both command and url")
	case typ == "stdio" && !hasCmd:
		return fail("type stdio needs a command")
	case (typ == "http" || typ == "sse" || typ == "streamable-http" || typ == "streamablehttp") && url == "":
		return fail("type %s needs a url", typ)
	case !hasCmd && url == "":
		return fail("needs a command or a url")
	}
	x := rs.Skgate
	if x == nil {
		x = &skgateExt{}
	}
	if hasCmd {
		u.Kind, u.Command = KindStdio, strings.TrimSpace(*rs.Command)
		if len(bytes.TrimSpace(rs.Args)) > 0 && string(bytes.TrimSpace(rs.Args)) != "null" {
			var args []json.RawMessage
			if err := json.Unmarshal(rs.Args, &args); err != nil {
				return fail("args must be a list")
			}
			for _, a := range args {
				s, ok := scalarString(a)
				if !ok {
					return fail("args must be strings")
				}
				u.Args = append(u.Args, s)
			}
		}
		env, err := stringMap(rs.Env, "env")
		if err != nil {
			return fail("%s", err)
		}
		u.Env = env
		if rs.Cwd != "" {
			it.Notes = append(it.Notes, "cwd ignored (each upstream runs in its own directory)")
		}
		if len(rs.Headers) > 0 && string(bytes.TrimSpace(rs.Headers)) != "null" {
			it.Notes = append(it.Notes, "headers ignored (stdio)")
		}
		u.Shell, u.Install, u.StartupSecs, u.IdleSecs = x.Shell, x.Install, x.StartupTimeout, x.IdleTimeout
		u.AutoUpdateSecs = x.AutoUpdate
		if x.Lifecycle != "" {
			u.Lifecycle = x.Lifecycle
		}
		if x.GitURL != "" {
			u.Kind, u.GitURL, u.GitRef = KindGit, x.GitURL, x.GitRef
		}
		if !u.Shell && strings.ContainsAny(u.Command, " \t") && len(u.Args) == 0 {
			return fail("the command contains spaces: put the program in command and its arguments in args, or set shell mode")
		}
	} else {
		u.Kind, u.URL, u.AuthKind = KindRemote, strings.TrimSpace(url), AuthNone
		if typ == "sse" {
			it.Notes = append(it.Notes, "type sse: skgate connects with Streamable HTTP; legacy SSE endpoints may not work")
		}
		if len(bytes.TrimSpace(rs.Env)) > 0 && string(bytes.TrimSpace(rs.Env)) != "null" {
			it.Notes = append(it.Notes, "env ignored (remote)")
		}
		hs, err := stringMap(rs.Headers, "headers")
		if err != nil {
			return fail("%s", err)
		}
		for _, h := range hs {
			if h.Value == "" {
				it.Notes = append(it.Notes, "header "+h.Name+" has no value: ignored")
				continue
			}
			if strings.EqualFold(h.Name, "authorization") && strings.HasPrefix(strings.ToLower(h.Value), "bearer ") && u.AuthKind == AuthNone {
				u.AuthKind, u.AuthValue = AuthBearer, strings.TrimSpace(h.Value[7:])
				continue
			}
			u.Headers = append(u.Headers, h)
		}
		u.HostOverride = x.HostOverride
		if u.AuthKind == AuthNone && len(u.Headers) == 0 {
			u.AuthKind = AuthAuto
			it.Notes = append(it.Notes, "no credentials given: auth set to auto-detect")
		}
	}
	if x.Aggregate != nil {
		u.IncludeInMCP, it.IncludeSet = *x.Aggregate, true
	}
	it.Upstream = u
	if err := u.Validate(); err != nil {
		it.Err = err.Error()
	}
	if it.IncludeSet && it.Upstream.IncludeInMCP != u.IncludeInMCP {
		it.Notes = append(it.Notes, "not on /mcp: only always-on servers can be")
	}
	it.Upstream = u
	return it
}

// StoreImport creates the valid items. Existing aliases and repeated names are reported as
// skipped, never overwritten. allowManaged false rejects managed items with a clear message.
func (s *Upstreams) StoreImport(items []ImportItem, allowManaged bool, include bool) []ImportResult {
	var out []ImportResult
	seen := map[string]bool{}
	for _, it := range items {
		r := ImportResult{Name: it.Name, Alias: it.Upstream.Alias, Notes: it.Notes}
		switch {
		case it.Err != "":
			r.Status, r.Detail = ImportInvalid, it.Err
		case it.Upstream.Managed() && !allowManaged:
			r.Status, r.Detail = ImportInvalid, "managed upstreams are not available in the slim image"
		case seen[it.Upstream.Alias]:
			r.Status, r.Detail = ImportSkipped, "another entry maps to the same alias"
		default:
			if _, exists := s.Get(it.Upstream.Alias); exists {
				r.Status, r.Detail = ImportSkipped, "alias already exists"
				break
			}
			u := it.Upstream
			if !it.IncludeSet {
				u.IncludeInMCP = include
			}
			if err := s.Create(u); err != nil {
				r.Status, r.Detail = ImportInvalid, err.Error()
			} else {
				r.Status = ImportCreated
			}
		}
		seen[it.Upstream.Alias] = true
		out = append(out, r)
	}
	return out
}

// ExportJSON renders upstreams in the mcpServers shape. Secret values (env values, header values,
// bearer tokens, the git token) are never written: names are kept with empty values.
func ExportJSON(ups []Upstream) []byte {
	servers := map[string]any{}
	for _, u := range ups {
		if u.IsOpenAPI() {
			continue // a REST API with its description is not an MCP server entry; export would lose the tools
		}
		e := map[string]any{}
		x := skgateExt{Install: u.Install, Shell: u.Shell, StartupTimeout: u.StartupSecs, IdleTimeout: u.IdleSecs, AutoUpdate: u.AutoUpdateSecs}
		if u.Lifecycle == managed.Always {
			x.Lifecycle = u.Lifecycle
		}
		switch u.KindOrRemote() {
		case KindRemote:
			e["type"] = "http"
			e["url"] = u.URL
			h := map[string]string{}
			if u.AuthKind == AuthBearer {
				h["Authorization"] = ""
			}
			if u.AuthKind == AuthHeader && u.AuthName != "" {
				h[u.AuthName] = ""
			}
			for _, kv := range u.Headers {
				h[kv.Name] = ""
			}
			if len(h) > 0 {
				e["headers"] = h
			}
			x.HostOverride = u.HostOverride
		default:
			e["type"] = "stdio"
			e["command"] = u.Command
			if len(u.Args) > 0 {
				e["args"] = u.Args
			}
			if len(u.Env) > 0 {
				env := map[string]string{}
				for _, kv := range u.Env {
					env[kv.Name] = ""
				}
				e["env"] = env
			}
			if u.Kind == KindGit {
				x.GitURL, x.GitRef = u.GitURL, u.GitRef
			}
		}
		if !u.Enabled {
			e["disabled"] = true
		}
		inc := u.IncludeInMCP
		x.Aggregate = &inc
		e["skgate"] = x
		servers[u.Alias] = e
	}
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	return append(b, '\n')
}
