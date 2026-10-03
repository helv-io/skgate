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
- **Admin access.** Every user the OIDC provider lets in is an admin, unless `OIDC_ALLOWED_EMAILS` or `OIDC_ALLOWED_GROUPS` is set. skgate logs `all OIDC users are admins (no allow-list set)` at start when it is configured without either. There are no roles.
- **Redirect origins.** Any https origin and loopback http are accepted; there is no allowlist. Anyone who can reach `/register` can register a client pointing at their own site, so an attacker must trick a signed-in admin into approving it. Consent (on by default) shows the client name and redirect host before approving.
- **Admin UI.** CSRF token on every POST; CSP `default-src 'self'` (no inline scripts or styles); `X-Frame-Options: DENY`. Notifications use one-shot signed HttpOnly cookies, never URL parameters.
- **Managed upstreams** (full image) execute admin-supplied commands: command execution is an admin capability. Use the `slim` image if you do not want it. See [Safety](mcp.md#safety).
- **Secrets.** Keys, upstream credentials, headers, env values and Grok tokens are displayed masked (`************abcd`); upstream secrets are encrypted at rest. Logs never contain tokens, client secrets, codes, cookies or credential headers; query values are redacted and log values are quoted.
- `?key=` is off by default because URLs leak into logs.
- **Grok tokens** (access, refresh and id) are sealed at rest with `SECRETS_KEY`, or with the key file next to the database when it is unset. Plaintext tokens from earlier versions are sealed automatically at startup.
- **Consent.** `MCP_OAUTH_REQUIRE_CONSENT` defaults to `true`.
- **Managed runners** (servers, install and git steps) run as `nobody` (65534) by default, each upstream with its own writable work, home, tmp and cache directories, so a compromised runner cannot read `SECRETS_KEY`, the key file or the database. skgate itself keeps only the capabilities needed to start and stop them. If it is started as a non-root user without them (for example `user:` in compose), it cannot switch users: runners then run as skgate's own user and a warning is logged.
- **Runner identity.** skgate refuses to start as `nobody` or `nogroup`, or with `PUID` or `PGID` set to 65534; it needs access to `SECRETS_KEY` and the database, which runners must not have.
- **`/mcp`** includes only always-on managed upstreams. On-demand ones are excluded automatically, so `/mcp` does not start every process; the upstream form says so next to the option.
- **Key limits** (optional, per virtual key; both default to unlimited). A rate limit allows N requests per minute (fixed one-minute window, kept in memory) and answers `429` with `Retry-After` beyond it. A hard stop ends the key at a total number of successful requests: from then on every request gets `429` until the stop is raised or cleared. Rejected requests do not count. Set them when creating a key or with **limits** on the key's row.

### Logging

Requests to `/authorize`, `/token`, `/register`, `/.well-known/*`, `/admin/oidc/*`, `/mcp`, `/sse` and `/messages` are logged as one line with `method`, `path`, `status`, `dur`, `client_id`, `redirect_host`, `ua`, `origin`, upstream fields and, on every 4xx/5xx, a `reason`. `LOG_LEVEL=debug` adds the redacted query, remote address, credential carriers present (never values), response size and notes.
