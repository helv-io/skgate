# Operations

Part of the [skgate README](../README.md).

## Image tags

Image: `ghcr.io/helv-io/skgate`.

| Tag | Contents |
| --- | --- |
| `latest` | Full: proxy + managed upstreams. Adds Node.js (npm/npx), Python 3 (pip), uv/uvx, git and tini as PID 1. |
| `slim` | Proxy only: MCP and provider proxying. No runtimes; managed upstreams are unavailable. |

Release tags add versioned names: `X.Y.Z` and `vX.Y.Z` (full), `X.Y.Z-slim` and `vX.Y.Z-slim` (slim). Every build also gets `<sha>` and `<sha>-slim`. Both images use the same root start, `PUID`/`PGID` drop, `/data` volume and `/skgate healthcheck`.

## Upgrading

Back up `/data` (`skgate.db` and `secrets.key`), then:

```sh
docker compose pull && docker compose up -d
```

The SQLite schema migrates on start. Pin `X.Y.Z` instead of `latest` to control when you move.

## Security notes

- **`/authorize`** never auto-approves. Order: check `client_id`, exact `redirect_uri`, redirect syntax (https, or http on loopback), `response_type=code` and PKCE S256; require OIDC (refuse if unconfigured); require an admin session (else login, then resume); apply `OIDC_ALLOWED_*`; show consent (on by default, `MCP_OAUTH_REQUIRE_CONSENT=false` turns it off); issue the code with `state` preserved. The OIDC subject and email are recorded on the code and tokens.
- **Redirect origins.** Any https origin and loopback http are accepted; there is no allowlist. Anyone who can reach `/register` can register a client pointing at their own site, so an attacker must trick a signed-in admin into approving it. Consent (on by default) shows the client name and redirect host before approving.
- **Admin UI.** CSRF token on every POST; CSP `default-src 'self'` (no inline scripts or styles); `X-Frame-Options: DENY`. Notifications use one-shot signed HttpOnly cookies, never URL parameters.
- **Managed upstreams** (full image) execute admin-supplied commands: command execution is an admin capability. Use the `slim` image if you do not want it. See [Safety](mcp.md#safety).
- **Secrets.** Keys, upstream credentials, headers, env values and Grok tokens are displayed masked (`************abcd`); upstream secrets are encrypted at rest. Logs never contain tokens, client secrets, codes, cookies or credential headers; query values are redacted and log values are quoted.
- `?key=` is off by default because URLs leak into logs.

### Logging

Requests to `/authorize`, `/token`, `/register`, `/.well-known/*`, `/admin/oidc/*`, `/mcp`, `/sse` and `/messages` are logged as one line with `method`, `path`, `status`, `dur`, `client_id`, `redirect_host`, `ua`, `origin`, upstream fields and, on every 4xx/5xx, a `reason`. `LOG_LEVEL=debug` adds the redacted query, remote address, credential carriers present (never values), response size and notes.
