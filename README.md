<p align="center"><img src="docs/img/logo.svg" width="72" alt="skgate logo"></p>
<h1 align="center">skgate</h1>

<p align="center">Serve your MCP servers and REST APIs from one OAuth-protected gateway, and use your Grok subscription or another AI provider as an OpenAI-compatible API.</p>

<p align="center">Yes, all MCP servers: everyone's welcome. skgate can run them for you too, so no more stacks.</p>

[![skgate demo](https://img.youtube.com/vi/57oxqkjzb4w/maxresdefault.jpg)](https://youtu.be/57oxqkjzb4w)

## Name Origin

**skgate** /ɛsˈkɑːɡeɪt/ (ess-KAH-gate)

"sk" is what most AI API keys start with, or so I perceive it, and "gate" is for gateway. Bit rubbish as names go, but it's ours.

The plane in the logo is an inside joke. The public wouldn't understand it, and I'm not about to explain it. Sorry.

## Quick start

Before you start: an [OIDC provider](#oidc-setup) with a confidential client for skgate (admin login is OIDC only). Just trying it on one machine? [docs/quickstart.md](docs/quickstart.md) runs skgate with a bundled provider and no accounts.

Grok is the best fit: works with your subscription, no API key. But skgate also speaks to OpenAI, Anthropic, Gemini, Mistral, DeepSeek, Groq, OpenRouter, Ollama and any OpenAI-compatible endpoint. Point your apps at one skgate URL and switch their AI provider in one place, with no app changes.

```yaml
# docker-compose.yml
services:
  skgate:
    container_name: skgate
    image: ghcr.io/helv-io/skgate:latest
    restart: always
    ports:
      - 8080:8080
    environment:
      - PUBLIC_URL=https://skgate.example.com
      - OIDC_ISSUER=https://auth.example.com
      - OIDC_CLIENT_ID=skgate
      - OIDC_CLIENT_SECRET=change-me
    volumes:
      - ./data:/data
    healthcheck:
      test: ["CMD", "/skgate", "healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
```

1. `docker compose up -d`
2. Open `https://skgate.example.com/admin` and sign in through your OIDC provider.
3. **status** > Grok > **Sign in**: open the shown address, enter the code, approve.
4. **keys** > enter a name > **Create key**. Copy the `sk-...` key; it is shown once.
5. Use it: base URL `https://skgate.example.com/v1`, API key `sk-...` (see Examples).
   - The base URL is forgiving: `/v1`, `/api`, `/api/v1` and the bare host all reach the same API, so use whichever form your client expects.

Image tags:

- `latest`: proxy + managed MCP servers (Node.js, Python, uv, .NET, Go, git)
- `slim`: proxy only

Upgrade: back up `./data`, then `docker compose pull && docker compose up -d`.

## Examples

<details><summary>curl: models and a chat completion</summary>

```sh
export SKGATE=https://skgate.example.com KEY=sk-...
curl -s $SKGATE/v1/models -H "Authorization: Bearer $KEY"
curl -s $SKGATE/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"<id from /v1/models>","messages":[{"role":"user","content":"Say hi"}]}'
```

</details>

<details><summary>OpenAI client</summary>

```sh
export OPENAI_BASE_URL=https://skgate.example.com/v1 OPENAI_API_KEY=sk-...
```

```python
from openai import OpenAI

client = OpenAI()  # reads the two variables above
r = client.chat.completions.create(model="<id from /v1/models>", messages=[{"role": "user", "content": "Say hi"}])
print(r.choices[0].message.content)
```

</details>

<details><summary>Model alias: one name that always points at the newest model</summary>

Map `grok-latest` to the latest available model. Change the target in this one place and every app using `grok-latest` is upgraded at once, with no client config changes.

Grok > **Details** > Model aliases: **New alias** `grok-latest`, **Target model** the newest model in the list (for example `grok-4.7`), **Add alias**.

```text
client sends   {"model": "grok-latest", ...}
skgate sends   {"model": "grok-4.7", ...}
```

Aliases are listed first in `/v1/models`.

</details>

<details><summary>Add an MCP server with Suggest configuration</summary>

Needs the `latest` image and an MCP helper model from any ready provider. The first time, **Pick MCP helper model** next to the button opens the model picker right on the page.

**mcp upstreams** > **Add upstream** > Type `managed (package or repository)`:

1. **MCP source URL / package**, one of:

   | Source | Runs as |
   | --- | --- |
   | `@modelcontextprotocol/server-everything` | npm package, `npx` |
   | `pypi:mcp-server-time` | PyPI package, `uvx` |
   | `https://github.com/example-org/notes-mcp` | git repo: clone, install, run (private: **Access token**) |

2. **Suggest configuration**. skgate fetches the README and manifests (`package.json`, `pyproject.toml`, `server.json`), the MCP helper model proposes command, args, install step and env names (marked secret or not, required or optional), and the **Manual configuration** fields are filled in with a confidence and any warnings. Nothing is saved yet. Point it at the repo and fill in the variables it needs.
3. Set an alias, fill in the variables you need (empty ones are not passed to the server), **Save**. The server is at `https://skgate.example.com/mcp/<alias>`.

If the button is greyed out, hover it: use **Pick MCP helper model** beside it, or add a provider on **status** first.

</details>

<details><summary>MCP client: one upstream or all of them</summary>

| URL | Serves |
| --- | --- |
| `https://skgate.example.com/mcp/<alias>` | One upstream; tool names unchanged |
| `https://skgate.example.com/mcp` | Every upstream marked **In /mcp**; tools prefixed `<alias>-` |

Hosted connectors use OAuth (leave client ID and secret empty). Scripts and CLIs send a key:

```json
{"mcpServers": {"skgate": {"type": "http", "url": "https://skgate.example.com/mcp/<alias>",
  "headers": {"Authorization": "Bearer sk-..."}}}}
```

</details>

<details><summary>On-demand MCP servers: no RAM while idle</summary>

On a RAM-constrained homelab, idle MCP servers should cost nothing. Managed servers (`npx`, `uvx`, git) are child processes of skgate. By default (**Lifecycle** `on-demand`) one starts on its first request and stops after 10 minutes without requests; the next request starts it again.

```text
before   3 MCP servers = 3 containers, always running
after    1 skgate container; 0 server processes while idle, 1 per server in use
```

```text
stopped  --request-->  starting  -->  running  --10 min idle-->  stopped
```

- Default is on-demand; **Lifecycle** `always-on` starts the server at boot instead.
- The first request after a stop waits until the server is up.
- A server with a request in flight is never stopped.
- Idle time is `idleTimeoutSeconds` in import JSON (default 600; not in the form):

```json
{"mcpServers": {"time": {"command": "uvx", "args": ["mcp-server-time"], "skgate": {"idleTimeoutSeconds": 120}}}}
```

- The aggregated `/mcp` includes remote, OpenAPI and always-on upstreams marked **In /mcp**. On-demand servers are left out, so `/mcp` never starts them. Point a client at `/mcp/<alias>` to use one.
- Remote and OpenAPI upstreams have no process; there is nothing to idle.
- An admin **Stop** keeps a server stopped until **Start** or **Restart**.

</details>

<details><summary>Managed MCP server: npx (stdio)</summary>

**mcp upstreams** > **import JSON** > paste > **Import**. Needs the `latest` image.

```json
{"mcpServers": {"everything": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-everything"]}}}
```

Served at `https://skgate.example.com/mcp/everything`. For Python servers use `"command": "uvx", "args": ["<package>"]`.

</details>

<details><summary>Managed MCP server: git repository</summary>

**mcp upstreams** > **import JSON** > paste > **Import**. skgate clones the repo, runs `install`, then the command.

```json
{"mcpServers": {"notes": {"command": "node", "args": ["server.js"],
  "skgate": {"gitUrl": "https://github.com/example-org/notes-mcp.git", "gitRef": "main", "install": "npm ci"}}}}
```

</details>

<details><summary>Remote MCP server</summary>

**mcp upstreams** > **Add upstream** > Type `remote (URL)`, or import:

```json
{"mcpServers": {"docs": {"type": "http", "url": "https://mcp.example.com/mcp",
  "headers": {"Authorization": "Bearer ..."}}}}
```

</details>

## Features

| Feature | What it does |
| --- | --- |
| Grok sign-in | Device code or browser paste-back; tokens refresh |
| Other providers | OpenAI, Anthropic, Gemini and more by API key ([docs](docs/api.md)) |
| Model aliases | `grok-latest` maps to the newest model; change the target once |
| API | `/v1`, `/api/v1`, `/api`, no prefix; SSE ([docs](docs/api.md)) |
| Virtual keys | Hashed; tokens in/out per key ([docs](docs/keys-and-clients.md)) |
| MCP | Remote, stdio and git servers behind OAuth 2.1 ([docs](docs/mcp.md)) |
| REST APIs as tools | Give an OpenAPI description, pick the operations, and MCP clients get them as tools ([docs](docs/openapi.md)) |

## Comparison

| Provider | Own subscription sign-in in third-party tools | In skgate |
| --- | --- | --- |
| xAI Grok | ✅ announced for OpenCode, more planned [1] | ✅ (independent, not an xAI product) |
| OpenAI | ✅ "Sign in with ChatGPT" since 2026-09-29; hosted apps need approval [2] | ❌ |
| Anthropic Claude | ❌ not offered to third parties [3] | ❌ use the API |
| Google Gemini | ❌ CLI login not for reuse [4] | ❌ use an API key |
| GitHub Copilot | ⚠️ OpenCode partnership only [5] | ❌ |

As of 2026-10-02; check each provider's terms.

<details><summary>Sources</summary>

1. xAI, [Use Grok in OpenCode](https://x.ai/news/grok-opencode)
2. OpenAI, [Sign in with ChatGPT](https://developers.openai.com/siwc/token-sharing-open-source)
3. Anthropic, [Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)
4. Google, [Gemini CLI terms](https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/tos-privacy.md)
5. GitHub, [Copilot now supports OpenCode](https://github.blog/changelog/2026-01-16-github-copilot-now-supports-opencode/)

</details>

## Configuration

Set under `environment:` (or `env_file`); placeholders in [`.env.example`](.env.example).

| Variable | Default | Purpose |
| --- | --- | --- |
| `PUBLIC_URL` | `http://localhost:8080` | Public origin, no trailing slash. |
| `OIDC_ISSUER` | | Issuer URL, equal to the provider's discovery `issuer`. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | | Confidential client credentials. |
| `OIDC_SCOPES` | `openid profile email groups` | Requested scopes. |
| `OIDC_REDIRECT_URL` | `PUBLIC_URL/admin/oidc/callback` | Callback registered at the provider. |
| `OIDC_ALLOWED_EMAILS`, `OIDC_ALLOWED_GROUPS` | empty | Comma lists limiting who is admin. |
| `MCP_OAUTH_REQUIRE_CONSENT` | `true` | Approve/Deny page after login at `/authorize`. |
| `SECRETS_KEY` | random `secrets.key` file | Encrypts stored upstream secrets. 32-byte base64 or a passphrase. |
| `GITHUB_TOKEN` | empty | Optional GitHub token for **Suggest** when it reads a repository. An upstream's own access token takes precedence. Raises GitHub's rate limit. |
| `UPDATE_CHECK` | `true` | The version in the header turns yellow when a newer release exists; `false` turns the check off. |
| `LISTEN_ADDR` | `:8080` | Listen address. |
| `DB_PATH` | `/data/skgate.db` | SQLite file. |
| `LOG_LEVEL` | `info` | `info` or `debug`. |
| `LOG_LINES` | `1000` | Lines of output kept per managed process (its current and previous run, at most 512 KB). |
| `TZ` | `UTC` | Time zone for the UI and logs, for example `America/New_York`. |
| `PUID`, `PGID` | `1000` | Run-as ids; never `0` or `65534`. |
| `MANAGED_DIR` | `/data/managed` | Work dirs and clones of managed upstreams. |
| `MANAGED_MAX_PROCS` | `0` | Concurrent managed processes; `0` is unlimited. |

Grok needs no variables; its base URL and aliases are in its **Details** dialog.

Everything lives in `/data` (`skgate.db`, `secrets.key`): back up both.

## Reverse proxy

Set `PUBLIC_URL` to the public https origin. No forward-auth on `/v1`, `/api`, `/mcp`, `/sse`, `/messages`, `/authorize`, `/token`, `/register`, `/.well-known`. Only Traefik is tested by the author; [open an issue](https://github.com/helv-io/skgate/issues) with feedback.

<details><summary>Traefik</summary>

```yaml
services:
  skgate:
    container_name: skgate
    image: ghcr.io/helv-io/skgate:latest
    restart: always
    network_mode: web   # existing Docker network shared with Traefik; no ports needed
    environment:
      - PUBLIC_URL=https://skgate.example.com   # the public https origin
      - OIDC_ISSUER=https://auth.example.com
      - OIDC_CLIENT_ID=skgate
      - OIDC_CLIENT_SECRET=change-me
    volumes:
      - ./data:/data
    healthcheck:
      test: ["CMD", "/skgate", "healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
    labels:
      # no forward-auth middleware on /v1, /api, /mcp, /sse, /messages, /authorize, /token, /register, /.well-known
      - traefik.enable=true
      - traefik.http.routers.skgate.rule=Host(`skgate.example.com`)
      - traefik.http.routers.skgate.entryPoints=websecure
      - traefik.http.services.skgate.loadbalancer.server.port=8080
```

</details>

<details><summary>nginx</summary>

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name skgate.example.com;
    ssl_certificate     /etc/ssl/skgate/fullchain.pem;
    ssl_certificate_key /etc/ssl/skgate/privkey.pem;

    client_max_body_size 32m;                         # skgate accepts up to 32 MB

    location / {
        proxy_pass http://skgate:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;                  # keep Host
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;                          # SSE streaming
        proxy_read_timeout 3600s;                     # long streams
        proxy_send_timeout 3600s;
    }
}
```

</details>

<details><summary>Caddy</summary>

```caddyfile
skgate.example.com {
    # Host and X-Forwarded-* are set by default; no body limit, no response timeout
    reverse_proxy skgate:8080 {
        flush_interval -1                             # SSE streaming
    }
}
```

</details>

<details><summary>HAProxy</summary>

```haproxy
defaults
    mode http
    timeout connect 5s
    timeout client  1h                                # long streams
    timeout server  1h
    timeout tunnel  1h

frontend https
    bind :443 ssl crt /etc/haproxy/certs/skgate.pem alpn h2,http/1.1
    option forwardfor                                 # X-Forwarded-For; Host is kept, responses are not buffered
    http-request set-header X-Forwarded-Proto https
    default_backend skgate

backend skgate
    option httpchk GET /healthz
    server skgate skgate:8080 check
```

</details>

<details><summary>Apache</summary>

```apache
# a2enmod ssl proxy proxy_http headers
<VirtualHost *:443>
    ServerName skgate.example.com
    SSLEngine on
    SSLCertificateFile    /etc/ssl/skgate/fullchain.pem
    SSLCertificateKeyFile /etc/ssl/skgate/privkey.pem

    # keep Host
    ProxyPreserveHost On
    RequestHeader set X-Forwarded-Proto "https"
    # long streams
    ProxyTimeout 3600
    # flushpackets: no buffering (SSE)
    ProxyPass        / http://skgate:8080/ flushpackets=on
    ProxyPassReverse / http://skgate:8080/
</VirtualHost>
```

</details>

## OIDC setup

| Client setting | Value |
| --- | --- |
| Type | Confidential, `client_secret_basic` or `client_secret_post` |
| Flow | Authorization code with PKCE S256 |
| Redirect URI | `https://skgate.example.com/admin/oidc/callback` |
| Scopes | `openid profile email groups` (if `groups` is rejected: `OIDC_SCOPES=openid profile email`) |
| ID token | Asymmetric signature (RS, PS, ES, EdDSA); discovery `issuer` equal to `OIDC_ISSUER` |

Without `OIDC_*` the admin answers 503. Every user your provider lets in is an admin: restrict the provider, or set `OIDC_ALLOWED_EMAILS` / `OIDC_ALLOWED_GROUPS`. Hints for Authelia, Authentik, Keycloak, Zitadel and Pocket ID: [docs/oidc.md](docs/oidc.md).

## Security notes

- Virtual keys are stored as SHA-256 hashes; upstream credentials are AES-256-GCM encrypted.
- `/authorize` needs an admin session.
- Managed upstreams run admin-supplied commands; use the `slim` image to disable them.
- A key can be allowed in the URL (`?key=`) for clients that cannot send headers. It is off per key by default, because URLs leak into logs, history and referrers.

More: [operations](docs/operations.md).

## Built with AI assistance

skgate is built with AI assistance: coding agents write much of the code, tests and docs. The author reviews the changes and runs skgate.

## Development

```sh
go build ./... && go vet ./... && go test ./...
```

[docs/development.md](docs/development.md). Contributors and agents: [AGENT.md](AGENT.md).

## License

MIT, see [LICENSE](LICENSE).
