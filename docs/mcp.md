# MCP upstreams and aggregation

Admin: upstreams. Part of the [skgate README](../README.md).

## Upstreams

Managed in the admin UI. Each has an alias (`a-z`, `0-9`, `-`), a type, and two toggles: **enabled** and **In /mcp**. The add form pre-selects the last chosen **In /mcp** value.

| Type | What skgate does |
| --- | --- |
| Remote | Proxies to a Streamable HTTP URL with outbound auth, optional custom headers and host override. |
| Managed | Runs an MCP server from a package or a git repository as a child process and bridges its stdin/stdout to HTTP. [Managed upstreams](#managed-upstreams). |

All types are served the same way: `/mcp/<alias>` (unprefixed), the aggregating `/mcp` (prefixed `<alias>-`), Test, keys and OAuth.

### Outbound auth modes

| Mode | Sends |
| --- | --- |
| `auto` (default) | Probes with an MCP `initialize` in order `none`, `bearer`, `header` and keeps the first that answers. |
| `none` | No credentials. |
| `bearer` | `Authorization: Bearer <value>`. |
| `header` | `<name>: <value>`. The header name field appears for `header` and `auto` only. |
| `passthrough` | The client's `X-Upstream-Authorization` value as `Authorization`, otherwise nothing. |

Auto detection runs on save, on **detect**, and lazily on first use. 401/403 count as failure. An upstream that only offers OAuth is reported as `oauth (unsupported)` and gets no credentials. Editing URL, credentials, header name or host override clears the result. Existing upstreams keep their mode.

**Host override** (`hostname[:port]`, under **Advanced** in the form; shown in the alias's hover text when set) replaces the outbound `Host` header, for servers that validate it. Probes and Test honor it.

**Test** runs `initialize`, `notifications/initialized` and `tools/list` with the effective auth and shows status, auth, latency, server, protocol and tools (first 100). Passthrough upstreams are tested without credentials.

**Custom headers** (remote): name/value rows (up to 32), sent on every outbound request after the auth header. Values are encrypted at rest and shown masked.

### Trailing slashes and redirects

Applies to remote upstreams only.

- Inbound paths behave the same with or without a trailing slash; skgate never redirects a client for it. Admin pages (`/admin/...`) do too.
- Outbound redirects (301/302/303/307/308) are followed inside skgate: method and body kept, at most 5 hops, same host only. They are never passed to the client.
- On a failure, 404 or 405, skgate retries once with the trailing slash toggled. When that works, the stored URL is rewritten to the working spelling; nothing about it is shown in the UI.

## Managed upstreams

skgate can run MCP servers itself, so you do not need one container per server. A managed upstream is a child process speaking newline-delimited JSON-RPC on stdin/stdout; skgate bridges it to Streamable HTTP.

Always available in the full image (`latest`); there is no switch. In the `slim` image the managed forms are disabled with a notice, creating or starting one is refused, requests to managed aliases answer 503, and nothing is ever spawned.

### Source and suggestions

The managed form has two main fields: **MCP source URL / package** and **Access token** (private repositories). **Suggest configuration** fills the manual fields from them; nothing is saved until you submit the form. The manual fields (command, arguments, environment, install, timeouts) are always available, with or without an account.

| Source | Result |
| --- | --- |
| Git URL (GitHub, GitLab, Gitea/Forgejo, Bitbucket; `/tree/<ref>/<dir>` accepted) | Git upstream. Node, Python, .NET (`dotnet`) and Go (`go`) repositories are detected. |
| npm package (`pkg`, `@scope/pkg@1.2.3`, `npm:pkg`, npmjs.com URL) | Runs with `npx`. |
| PyPI package (`pkg==1.2.3`, `pypi:pkg`, pypi.org URL) | Runs with `uvx`. |
| crates.io, Go modules, Docker, NuGet, RubyGems, Maven, JSR/Deno | Refused with a message: the image has no `cargo`, `go`, `docker`, `gem`, `mvn` or `deno`, and NuGet packages are not run directly (point at the git repository instead). |

How Suggest configuration works:

- It uses the MCP helper model (pick it beside the button, or in the provider details dialog; names the provider offers as aliases of a model are listed next to it and are sent as chosen) and a fixed system prompt. Output is requested as a strict JSON schema and validated in Go; invalid output is rejected.
- skgate fetches the README and manifests itself (`package.json`, `pyproject.toml`, `server.json`, ...) through the host's API, with the token when given. The model sees those documents, never the token; the token is not logged.
- The model call is streamed, with the reasoning chosen in the MCP helper model dialog (default: the model decides). It ends after the helper timeout without data (default 120 seconds, set next to the model; for models that look like heavy reasoners the dialog suggests 600) or after 5 minutes overall, twice the timeout when that is longer; the progress line then shows a timed-out state with the stage and elapsed time and a **Lower reasoning** button. A provider that rejects `reasoning_effort` gets the request again without it.
- Validated suggestions use a command available on the host, name and pin the package, list only environment variables the documents mention with `YOUR_...` placeholders, and come with confidence and warnings.
- Disabled, with a tooltip, until you are signed in and an MCP helper model is picked. **Pick MCP helper model** next to it opens the picker in place; the button enables without a page reload.

### Kinds

**Command (stdio).**

| Field | Notes |
| --- | --- |
| Command | Required. A dropdown of the commands found on `PATH` at runtime (`npx`, `bunx`, `pnpm`, `npm`, `node`, `deno`, `uvx`, `uv`, `pipx`, `python3`, `python`, `dotnet`, `go`, `git`, `docker`, `sh`, `bash`; only those installed) or **Custom path…** for any other name or absolute path. Stored commands not in the list open as Custom. Executed directly (no shell). |
| Args | One per line. |
| Env | Name/value rows (up to 64), encrypted at rest, shown masked. |
| Shell mode | Opt-in. Runs `sh -c` on the command line. Off by default. |
| Install command | Optional. Runs once before start (again when it changes, or on **Update**), in the upstream's directory, limited to 15 minutes. |
| Lifecycle | `on-demand` (start on first request, stop after 10 minutes idle) or `always` (start at boot). |
| Startup timeout | Not in the form. Seconds until the child must answer `initialize`; 60 unless the suggestion helper or an imported `startupTimeoutSeconds` sets it. A stored value is kept on save. |

**Git repository.** Same as above, with the repository URL in the source field (https, http or local path; no ssh), ref (branch or tag, default the remote's HEAD) and an optional token for private repos. skgate clones into `<MANAGED_DIR>/<alias>/repo` (shallow), runs the install step (for example `npm ci`, `uv sync`, `pip install -r requirements.txt`), then the command with the repo as working dir. **Update** fetches the ref, reruns install and restarts ([updates](#updates)). The token is encrypted at rest and passed to git only as an `http.extraHeader` through its environment, never in arguments or logs.

Env and header lists start with one row; **Add** appends rows, **Delete** removes an added row, and blank rows are ignored on save. A masked value left unchanged keeps the stored one.

### Example: commands

```
# Command: npx            Args: -y, @modelcontextprotocol/server-everything
# Command: uvx            Args: mcp-server-time
```

### JSON import and export

Admin: upstreams, **Import JSON**. Paste a `{"mcpServers": {...}}` object, a bare name-to-server map, or a single server object. Aliases come from the keys, lowercased and sanitized. Each entry is reported as created, skipped (alias exists; nothing is overwritten) or invalid with the reason.

```json
{
  "mcpServers": {
    "time": { "command": "uvx", "args": ["mcp-server-time"] },
    "everything": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "env": { "EXAMPLE_TOKEN": "..." }
    },
    "remote": {
      "type": "http",
      "url": "https://mcp.example.com/mcp",
      "headers": { "Authorization": "Bearer ..." }
    }
  }
}
```

- `type` is `stdio`, `http` or `sse`. Without it, `command` means stdio and `url` means remote. `sse` is imported as Streamable HTTP with a note.
- `cwd` is ignored; every command runs in its own directory (below).
- `headers` with a `Bearer` authorization become bearer auth; other headers become custom headers.
- An `skgate` block carries settings other clients lack: `lifecycle`, `shell`, `install`, `startupTimeoutSeconds`, `gitUrl`, `gitRef`, `autoUpdateSeconds`, `hostOverride`, `includeInAggregate`. `disabled: true` imports the upstream disabled.
- Managed entries are refused while managed upstreams are off.
- **Export JSON** writes the same shape. Secret values (env, headers, tokens) are left empty.

### Process lifecycle

| Status | Meaning |
| --- | --- |
| `stopped` | Not running (on-demand idle, or stopped by an admin). |
| `starting` | Spawned; install or `initialize` in progress. |
| `running` | Answering. Shows PID, uptime and restart count. |
| `failed` | Start error or crash loop. Shows the last error. |

- **Start, Stop, Restart, Update** and, for git, **Check** are on the upstream list and the process page. An admin **Stop** holds the process stopped until Start or Restart.
- A crashed child restarts with exponential backoff (1 s doubling to 30 s). Five crashes in a row mark it `failed`; a run of 30 s or more resets the count. A failed upstream is retried by a request at most every 30 s.
- Editing the command, args, env, install step or repository replaces the process.
- Stop sends SIGTERM to the child's process group, then SIGKILL after 5 seconds, so grandchildren do not leak. The same happens for every child when skgate exits.
- The status is a compact pill; PID, uptime, restarts and the last error are in its hover text.
- `MANAGED_MAX_PROCS` caps concurrent children (default `0`, unlimited). At the cap, further starts fail with a clear error until one stops.
- **Test** starts the process if needed, then runs `initialize` and `tools/list`.
- **Logs** (process page) shows the last lines of the child's stderr from a bounded in-memory ring (2000 lines). Lines are also written to skgate's log as `managed[alias] stderr: ...`. Env values and the git token are redacted.

### Updates

- **Git.** skgate records the installed commit and shows it (short SHA and ref) on the upstream list and the process page. Every 30 minutes, and on **Check**, it asks the remote where the ref points (`git ls-remote`, no clone; same token handling as the other git steps). A branch or default ref that moved shows **update available** and emphasizes **Update**, which stays usable at any time. Tags and commit IDs are shown as **pinned** and are never "ahead".
- **Command.** **Update** clears that upstream's package cache and restarts it, so `npx` and `uvx` resolve the package again. Caches are per upstream, in `<db dir>/cache/managed/<alias>`; clearing one never touches another. An exact version (`pkg@1.2.3`, `pkg==1.2.3`) is shown as **pinned** and stays that version.
- **Rollback.** An update fetches while the old version keeps running. If the fetch, install or start fails, the previous commit (or package cache) is restored and restarted, and the error is shown on the process page and logged. Updating a stopped upstream does not start it.
- **Auto-update** is off by default. Choose an interval (15 minutes to weekly, minimum 5 minutes) in the upstream form. It applies to git branches and default refs that moved, and to unpinned packages run by `npx`, `bunx`, `pnpm dlx`, `uvx`, `uv tool run` or `pipx run`. It never touches tags, commit IDs, pinned versions or other commands. Each update is logged as `managed[alias]: update auto old=<version> new=<version>`; manual ones as `update old=... new=...`; failures as `update failed: ...`.
- **Safety.** An enabled auto-update runs newly published third-party code without review, with the upstream's environment and secrets. Pin a version or use a tag where that is not acceptable.

### Bridging

Many HTTP clients share one child. skgate remaps JSON-RPC ids per call (and progress tokens) and routes each response back to its client session (`Mcp-Session-Id`). Supported: `initialize`, tools, resources, prompts, notifications, progress, cancellation, batches and server-to-client requests (sampling, roots, elicitation) sent to a session that declared the capability. Replies are JSON, or an SSE stream when the client asks for progress or supports server requests and accepts `text/event-stream`. `GET /mcp/<alias>` opens the server-to-client notification stream.

### Safety

- **Command execution is an admin capability.** Anyone who is admin can run commands in the container. Admin access is OIDC-protected and POST actions are CSRF-checked; restrict it accordingly.
- Children run as the same unprivileged uid as skgate (after the `PUID`/`PGID` drop), never root.
- Children get an allow-listed environment only: `PATH`, `LANG`, `LC_*`, `TZ`, CA and proxy variables, package cache variables, a per-alias `HOME` and `TMPDIR`, and the upstream's own env. skgate's `OIDC_*`, `SECRETS_KEY` and other settings are not passed.
- Commands are validated (non-empty, no control characters). No shell unless Shell mode is chosen.
- Stored secrets (env, headers, tokens, bearer/header auth values) are AES-256-GCM encrypted with `SECRETS_KEY`, or with `secrets.key` next to the database. Back the key up with the database; without it the secrets cannot be read.

### Data volume layout

```
/data/skgate.db             SQLite
/data/secrets.key           encryption key (when SECRETS_KEY is unset)
/data/managed/<alias>/work  working dir (stdio)
/data/managed/<alias>/repo  clone (git)
/data/managed/<alias>/home  HOME of the child
/data/managed/<alias>/tmp   TMPDIR of the child
/data/cache/managed/<alias>  package caches (npm, uv, pip, xdg), one directory per upstream
```

In the full image each child gets `NPM_CONFIG_CACHE`, `UV_CACHE_DIR`, `PIP_CACHE_DIR` and `XDG_CACHE_HOME` under its own cache directory (the image sets shared defaults for tools run by hand), so the rest of the filesystem can be read-only (mount `/data` writable). Deleting or disabling an upstream stops its process; its directory under `MANAGED_DIR` is left in place.

## Aggregation: `/mcp` vs `/mcp/<alias>`

| Endpoint | Behavior |
| --- | --- |
| `/mcp/<alias>` | Transparent, unprefixed proxy to one upstream. |
| `/mcp` | One MCP server merging every enabled upstream that is **In /mcp**. |

`/mcp` details:

- `initialize` is answered by skgate (`serverInfo.name` = `skgate`).
- Tools, prompts and resources are prefixed: `<alias>-<name>`; resource URIs use `<alias>+<uri>`. Prefixes are stripped on `tools/call`, `prompts/get` and `resources/read`; the longest matching alias wins.
- Each call uses the upstream's own effective auth and host override. `nextCursor` is followed (20 pages max).
- An upstream that is down or errors is skipped in lists and logged (`aggregate alias=... skipped=true reason=...`); the rest still answer. Unknown prefix: JSON-RPC `-32602`.
- One session (`Mcp-Session-Id`) per upstream is kept in memory and re-created if the upstream forgets it or its settings change.
- Request/response only: `GET /mcp` returns 405, `DELETE /mcp` is a no-op.
