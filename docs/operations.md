# Operations

Part of the [skgate README](../README.md).

## Image tags

Image: `ghcr.io/helv-io/skgate`.

| Tag | Contents |
| --- | --- |
| `latest` | Full: proxy + managed upstreams. Adds Node.js (npm/npx), Python 3 (pip), uv/uvx, git and tini as PID 1. |
| `slim` | Proxy only: MCP and provider proxying. No runtimes; managed upstreams are unavailable. |

Release tags add versioned names: `X.Y.Z` and `vX.Y.Z` (full), `X.Y.Z-slim` and `vX.Y.Z-slim` (slim). Every build also gets `<sha>` and `<sha>-slim`. Both images use the same root start, `PUID`/`PGID` drop, `/data` volume and `/skgate healthcheck`.

## MCP registry

Each stable release tag is listed in the [official MCP registry](https://registry.modelcontextprotocol.io) as `io.github.helv-io/skgate`, from the Release workflow's `mcp-registry` job after the images are published. The entry is `server.json`; both images carry the `io.modelcontextprotocol.server.name` label the registry checks. The job logs in with GitHub OIDC (no secret), fails when `server.json` does not match the tag, and skips a version that is already listed.

## Unraid

`unraid/skgate.xml` is a Community Apps template for the `ghcr.io/helv-io/skgate:latest` image: the web port (8080), the appdata folder (`/data`) and the OIDC settings for admin sign-in (`PUBLIC_URL`, `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`), with the optional ones under Advanced. To use it before it is listed, copy the file to `/boot/config/plugins/dockerMan/templates-user/skgate.xml` on the server and add the container from Docker, Add Container, and the `skgate` template. `ca_profile.xml` at the repository root is the maintainer profile (the Community Apps scanner looks for it there) and `unraid/skgate.png` the icon. skgate needs an OIDC provider and a public https address, as everywhere else. Questions and support: the [Unraid forum thread](https://forums.unraid.net/topic/200787-support-skgate-an-mcp-proxy-that-just-works/).

## Upgrading

Back up `/data` (`skgate.db` and `secrets.key`), then:

```sh
docker compose pull && docker compose up -d
```

The SQLite schema migrates on start. Pin `X.Y.Z` instead of `latest` to control when you move.

## Update hint

The version number in the admin header links to the GitHub repository. It turns yellow, with the new version in its tooltip, when GitHub has a newer stable release. skgate asks the public latest-release API without credentials, from the server, at most every six hours (an hour after a failure), keeps the answer in memory and never makes a page wait. Offline, rate limited or an unreadable answer simply shows no hint. `UPDATE_CHECK=false` turns the check off.

## Security notes

- **`/authorize`** never auto-approves. Order: check `client_id`, exact `redirect_uri`, redirect syntax (https, or http on loopback), `response_type=code` and PKCE S256; require OIDC (refuse if unconfigured); require an admin session (else login, then resume); apply `OIDC_ALLOWED_*`; show consent (on by default, `MCP_OAUTH_REQUIRE_CONSENT=false` turns it off); issue the code with `state` preserved. The OIDC subject and email are recorded on the code and tokens.
- **Admin access.** Every user the OIDC provider lets in is an admin, unless `OIDC_ALLOWED_EMAILS` or `OIDC_ALLOWED_GROUPS` is set. skgate logs `all OIDC users are admins (no allow-list set)` at start when it is configured without either. There are no roles.
- **Redirect origins.** Any https origin and loopback http are accepted; there is no allowlist. Anyone who can reach `/register` can register a client pointing at their own site, so an attacker must trick a signed-in admin into approving it. Consent (on by default) shows the client name and redirect host before approving (and the client ID host for metadata document clients).
- **Admin UI.** CSRF token on every POST; CSP `default-src 'self'` (no inline scripts or styles); `X-Frame-Options: DENY`. Notifications use one-shot signed HttpOnly cookies, never URL parameters.
- **Managed upstreams** (full image) execute admin-supplied commands: command execution is an admin capability. Use the `slim` image if you do not want it. See [Safety](mcp.md#safety).
- **Secrets.** Keys, upstream credentials, headers, env values and Grok tokens are displayed masked (`************abcd`); upstream secrets are encrypted at rest. Logs never contain tokens, client secrets, codes, cookies or credential headers; query values are redacted and log values are quoted.
- `?key=` is a per-key switch (a checkbox in the key's edit dialog, off by default) because URLs leak into logs, history and referrers. Only MCP endpoints accept it; a key without the switch gets 401 and the request log says why. Upgrading from the old global switch: if it was on, every existing key gets the switch once at startup; if off, none do. Keys created later start off.
- **Grok tokens** (access, refresh and id) are sealed at rest with `SECRETS_KEY`, or with the key file next to the database when it is unset. Plaintext tokens from earlier versions are sealed automatically at startup.
- **Consent.** `MCP_OAUTH_REQUIRE_CONSENT` defaults to `true`.
- **Managed runners** (servers, install and git steps) run as `nobody` (65534) by default, each upstream with its own writable work, home, tmp and cache directories, so a compromised runner cannot read `SECRETS_KEY`, the key file or the database. skgate itself keeps only the capabilities needed to start and stop them. If it is started as a non-root user without them (for example `user:` in compose), it cannot switch users: runners then run as skgate's own user and a warning is logged.
- **Runner identity.** skgate refuses to start as `nobody` or `nogroup`, or with `PUID` or `PGID` set to 65534; it needs access to `SECRETS_KEY` and the database, which runners must not have.
- **`/mcp`** includes only always-on managed upstreams. On-demand ones are excluded automatically, so `/mcp` does not start every process; the upstream form says so next to the option.
- **Key limits** (optional, per virtual key; a key has none until you set them). A **rate limit** allows N requests per minute (fixed one-minute window, kept in memory) and answers `429` with `Retry-After` beyond it; rejected requests do not count. An **expiration** ends the key at a moment you choose: from then on the key is refused with `401` and a message that says it expired, on `/v1` and on MCP alike, and the keys table shows it as `expired`. Type the expiration as plain text (`30d`, `2w`, `6 months`, `tomorrow`, `friday`, `2026-12-31`, `dec 31`, optionally with a time such as `5pm`); the field shows what it understood, in the server's time zone, before you save, and a date alone means the end of that day. Empty means it never expires, and moving the date brings an expired key back with the same secret. Set both under **Limits** when creating a key, or with **edit** on the key's row.

### Logging

Requests to `/authorize`, `/token`, `/register`, `/.well-known/*`, `/admin/oidc/*`, `/mcp`, `/sse` and `/messages` are logged as one line with `method`, `path`, `status`, `dur`, `client_id`, `redirect_host`, `ua`, `origin`, upstream fields and, on every 4xx/5xx, a `reason`. `LOG_LEVEL=debug` adds the redacted query, remote address, credential carriers present (never values), response size and notes.
