package directory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/helv-io/skgate/internal/config"
)

// toolList is the tools/list answer. The descriptions are what a client or a directory shows, so
// each one says what the tool does, what it takes and what it returns.
func toolList() []map[string]any {
	ro := func(title string) map[string]any {
		return map[string]any{"title": title, "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	list := func(item map[string]any) map[string]any { return map[string]any{"type": "array", "items": item} }
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return []map[string]any{
		{
			"name":  "about",
			"title": "About skgate",
			"description": "Describe skgate: what it is, the running version, the edition, the licence and where its source and docs live. " +
				"Takes no arguments. Returns one object with name, version, edition, summary, license, repository, docs and mode. " +
				"Read-only and offline: it answers from the binary alone and contacts nothing. " +
				"Call it first to learn what skgate is before using list_features or config_help.",
			"inputSchema": obj(map[string]any{}),
			"outputSchema": obj(map[string]any{
				"name":       str("Always skgate."),
				"version":    str("Release number, for example 0.27.3."),
				"edition":    str("full runs MCP servers for you, slim only proxies."),
				"summary":    str("One-paragraph description of skgate."),
				"license":    str("Licence of the source code."),
				"repository": str("Address of the source code."),
				"docs":       str("Address of the documentation folder."),
				"mode":       str("States that this is the read-only directory mode and not the gateway."),
			}, "name", "version", "edition", "summary", "license", "repository", "docs", "mode"),
			"annotations": ro("About skgate"),
		},
		{
			"name":  "list_features",
			"title": "List skgate features",
			"description": "List what skgate does, one entry per feature, optionally limited to one area. " +
				"Takes an optional area: mcp (MCP servers and tools), openapi (REST APIs as tools), llm_proxy (the OpenAI-compatible API, providers, aliases and keys), " +
				"auth (OAuth and sign-in) or deploy (running and configuring it). Without an area it returns every feature. " +
				"Returns an object with the area asked for ('all' by default) and a features array of id, area and summary. " +
				"An unknown area is an error that names the valid ones. Read-only and offline.",
			"inputSchema": obj(map[string]any{
				"area": map[string]any{"type": "string", "enum": areas(), "description": "Limit the list to one area. Omit it for all features."},
			}),
			"outputSchema": obj(map[string]any{
				"area": str("The area that was asked for, or all."),
				"features": list(obj(map[string]any{
					"id":      str("Short stable name of the feature."),
					"area":    str("Area the feature belongs to."),
					"summary": str("What the feature does, in one sentence."),
				}, "id", "area", "summary")),
			}, "area", "features"),
			"annotations": ro("List skgate features"),
		},
		{
			"name":  "config_help",
			"title": "Explain skgate settings",
			"description": "Explain the environment variables that configure a skgate server. " +
				"Takes an optional variable, an exact variable name such as PUBLIC_URL (case does not matter); without it the tool returns every variable. " +
				"Returns an object with a settings array of name, default and description, in the order of the README table. " +
				"Defaults are the documented values and no secret is ever included. An unknown name is an error that lists the valid names. " +
				"Read-only and offline: it shows documented defaults, reads no environment variable and cannot tell you the values of a running server.",
			"inputSchema": obj(map[string]any{
				"variable": map[string]any{"type": "string", "description": "Exact environment variable name, for example OIDC_ISSUER. Omit it for all variables."},
			}),
			"outputSchema": obj(map[string]any{
				"settings": list(obj(map[string]any{
					"name":        str("Environment variable name."),
					"default":     str("Documented default. Empty means unset."),
					"description": str("What the setting does."),
				}, "name", "default", "description")),
			}, "settings"),
			"annotations": ro("Explain skgate settings"),
		},
	}
}

// call runs one tool. ok is false for a tool that does not exist. A bad argument is a tool error
// (isError), so a model can read the message and retry.
func call(name string, args json.RawMessage) (res map[string]any, ok bool) {
	switch name {
	case "about":
		return reply(about()), true
	case "list_features":
		var a struct {
			Area string `json:"area"`
		}
		if msg := parseArgs(args, &a); msg != "" {
			return refuse(msg), true
		}
		out, err := listFeatures(a.Area)
		if err != "" {
			return refuse(err), true
		}
		return reply(out), true
	case "config_help":
		var a struct {
			Variable string `json:"variable"`
		}
		if msg := parseArgs(args, &a); msg != "" {
			return refuse(msg), true
		}
		out, err := configHelp(a.Variable)
		if err != "" {
			return refuse(err), true
		}
		return reply(out), true
	}
	return nil, false
}

func parseArgs(raw json.RawMessage, into any) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return "arguments must be an object of strings"
	}
	return ""
}

func reply(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": v}
}

func refuse(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

func about() map[string]any {
	return map[string]any{
		"name":       "skgate",
		"version":    config.Version,
		"edition":    config.Edition,
		"summary":    summary,
		"license":    "MIT",
		"repository": repoURL,
		"docs":       docsURL,
		"mode":       "directory mode (skgate stdio): read-only, offline, no sign-in. It describes skgate and does not run the gateway.",
	}
}

type feature struct {
	ID      string `json:"id"`
	Area    string `json:"area"`
	Summary string `json:"summary"`
}

var features = []feature{
	{"remote_upstreams", "mcp", "Serve a remote MCP server behind OAuth 2.1 at /mcp/<alias>, with outbound auth detected automatically."},
	{"managed_servers", "mcp", "Run stdio MCP servers from an npm or PyPI package or a git repository as child processes, started on demand and stopped when idle. Full image only."},
	{"aggregate_endpoint", "mcp", "One /mcp address lists the tools of every included upstream, each name prefixed with its alias."},
	{"suggest_configuration", "mcp", "Fill in an upstream from a repository or package address with a helper model."},
	{"live_tool_updates", "mcp", "Connected clients are told when an upstream's tools change and pick up the new tools without reconnecting."},
	{"tool_tester", "mcp", "Try any tool from the admin pages with a form made from its input schema."},
	{"rest_as_tools", "openapi", "Give an OpenAPI description, pick the operations, and MCP clients get them as tools."},
	{"openai_compatible_api", "llm_proxy", "One OpenAI-compatible address (/v1, /api/v1, /api) in front of your SI providers, with streaming."},
	{"model_aliases", "llm_proxy", "User-defined model names that point at a target model, so changing the target switches every app at once."},
	{"grok_subscription", "llm_proxy", "Sign in with a Grok subscription and use it as that API with no API key."},
	{"other_providers", "llm_proxy", "OpenAI, Anthropic, Gemini, Mistral, DeepSeek, Groq, OpenRouter, Ollama, LM Studio and any OpenAI-compatible endpoint, by API key."},
	{"virtual_keys", "llm_proxy", "Hashed keys for your apps, with tokens in and out counted per key."},
	{"oauth_for_mcp_clients", "auth", "OAuth 2.1 for MCP clients with dynamic client registration, client ID metadata documents and PKCE."},
	{"oidc_admin_login", "auth", "Admin sign-in through your OIDC provider, such as Authelia or Authentik. There are no local passwords."},
	{"docker_images", "deploy", "A full image with Node.js, Bun, Deno, Python, Go, .NET and git runtimes, and a slim proxy-only image."},
	{"single_data_folder", "deploy", "State lives in one /data folder (a SQLite file and an encryption key), so a backup is a copy of it."},
}

func areas() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range features {
		if !seen[f.Area] {
			seen[f.Area] = true
			out = append(out, f.Area)
		}
	}
	return out
}

func listFeatures(area string) (map[string]any, string) {
	area = strings.ToLower(strings.TrimSpace(area))
	list := []feature{}
	for _, f := range features {
		if area == "" || area == "all" || f.Area == area {
			list = append(list, f)
		}
	}
	if len(list) == 0 {
		return nil, fmt.Sprintf("unknown area %q, use one of: %s", clip(area), strings.Join(areas(), ", "))
	}
	if area == "" {
		area = "all"
	}
	return map[string]any{"area": area, "features": list}, ""
}

type setting struct {
	Name        string `json:"name"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

// settings mirrors the README configuration table; a test keeps the names equal.
var settings = []setting{
	{"PUBLIC_URL", "http://localhost:8080", "Public origin, no trailing slash."},
	{"OIDC_ISSUER", "", "Issuer URL of your OIDC provider, equal to the issuer in its discovery document."},
	{"OIDC_CLIENT_ID", "", "Client ID of the confidential OIDC client made for skgate."},
	{"OIDC_CLIENT_SECRET", "", "Client secret of that OIDC client. Set it in the environment of the server, never in a file you share."},
	{"OIDC_SCOPES", "openid profile email groups", "Scopes requested at sign-in."},
	{"OIDC_REDIRECT_URL", "PUBLIC_URL/admin/oidc/callback", "Callback address registered at the OIDC provider."},
	{"OIDC_ALLOWED_EMAILS", "", "Comma list of emails allowed to be admin. Empty means no limit by email."},
	{"OIDC_ALLOWED_GROUPS", "", "Comma list of groups allowed to be admin. Empty means no limit by group."},
	{"MCP_OAUTH_REQUIRE_CONSENT", "true", "Show an Approve or Deny page after sign-in when an MCP client asks for access."},
	{"SECRETS_KEY", "random secrets.key file", "Encrypts stored upstream secrets: a 32-byte base64 key or a passphrase."},
	{"GITHUB_TOKEN", "", "Optional GitHub token that Suggest uses to read a repository and that raises GitHub's rate limit."},
	{"UPDATE_CHECK", "true", "Turn the header's newer-release hint on or off. false turns it off."},
	{"LISTEN_ADDR", ":8080", "Address the HTTP server listens on."},
	{"DB_PATH", "/data/skgate.db", "Location of the SQLite file."},
	{"LOG_LEVEL", "info", "info or debug."},
	{"LOG_LINES", "1000", "Lines of output kept per managed process (its current and previous run, at most 512 KB)."},
	{"TZ", "UTC", "Time zone for the pages and logs, for example America/New_York."},
	{"PUID", "1000", "User id the server runs as. Never 0, 65534 or 61000-64999."},
	{"PGID", "1000", "Group id the server runs as. Same limits as PUID."},
	{"MANAGED_DIR", "/data/managed", "Work folders and clones of managed upstreams."},
	{"MANAGED_MAX_PROCS", "0", "Concurrent managed processes. 0 means unlimited."},
}

func configHelp(name string) (map[string]any, string) {
	name = strings.TrimSpace(name)
	list := []setting{}
	for _, s := range settings {
		if name == "" || strings.EqualFold(name, s.Name) {
			list = append(list, s)
		}
	}
	if len(list) == 0 {
		names := make([]string, 0, len(settings))
		for _, s := range settings {
			names = append(names, s.Name)
		}
		sort.Strings(names)
		return nil, fmt.Sprintf("unknown variable %q, use one of: %s", clip(name), strings.Join(names, ", "))
	}
	return map[string]any{"settings": list}, ""
}
