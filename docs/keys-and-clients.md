# Keys and OAuth clients

Part of the [skgate README](../README.md).

**Virtual keys** (admin: keys). Only a SHA-256 hash is stored; the token is shown once with a Copy button. Send as `Authorization: Bearer sk-...` or `X-API-Key` (or `?key=` if enabled), to `/v1` and MCP endpoints.

- **Usage** column: input and output tokens per key, abbreviated (`1.2M in / 340K out`); the tooltip has exact counts, API and MCP requests (last use is its own column). Recorded in the database (`key_usage`), written every 5 s and on shutdown, removed with the key.
  - API (`/v1`): tokens come from the provider's `usage` object, in JSON responses and in the last usage chunk of an event stream. Nothing is estimated and requests are never modified. A stream carries usage only if the client asks for it (for example `stream_options: {"include_usage": true}` on providers that need it); otherwise the call counts as a request without tokens. An aborted stream counts no tokens.
  - A request is a successful call that is not a read (model lists are not counted).
  - MCP: no tokens exist; authenticated requests made with a key are counted. OAuth access tokens are not tied to a key and are not counted.
- **Regenerate** replaces the secret of a record (label, created and last used are kept); the old token stops working immediately.
- **Revoke** disables a key. A revoked key that was never used is deleted at once; a used one is deleted 30 days after its last use. A sweep runs at startup and daily, and each purge is logged.

**OAuth clients** (admin: oauth clients). DCR clients (`/register`) and manual clients (fixed ID and secret, for clients without DCR) are listed with their last use at `/authorize` or `/token`. The newest 500 DCR clients are kept.

| Client | Setup |
| --- | --- |
| Hosted MCP connectors | URL `PUBLIC_URL/mcp/<alias>`, empty client ID and secret (DCR, public, PKCE). |
| Native / CLI | DCR with a loopback redirect (`127.0.0.1`, `localhost`, `[::1]`, any port). |
| No DCR | Manual client with its exact redirect URI. |
| Home Assistant | Steps below. |
| Scripts, mcp-proxy | Bearer key or `X-API-Key`. |
| SSE-only | `GET /sse[/alias]` then `POST /messages`, bridged to Streamable HTTP. |

Discovery: `PUBLIC_URL/.well-known/oauth-authorization-server`. Protected-resource metadata is served per endpoint (`/.well-known/oauth-protected-resource/mcp` and `/mcp/<alias>`, `resource` equal to the URL); there is none at the root.

PKCE: a `code_challenge` that is sent must be `S256` and is always verified. Without one, `/authorize` accepts only confidential clients (`client_secret_post`/`basic`, secret checked at `/token`) that have not used PKCE yet; after a client's first successful PKCE exchange it needs PKCE. Public and dynamically registered clients always need it.

**Home Assistant** (no DCR, sends no `code_challenge`):

1. Admin: OAuth clients, Create client, preset **Home Assistant** (redirect `https://my.home-assistant.io/redirect/oauth`, `client_secret_post`). Copy the client ID and secret.
2. Home Assistant: Settings, Devices & services, Application Credentials, add one for **Model Context Protocol** with that client ID and secret.
3. Add the Model Context Protocol integration with `https://<host>/mcp` or `https://<host>/mcp/<alias>` and approve the sign-in.

```sh
curl https://skgate.example.com/mcp/myalias -H 'Authorization: Bearer sk-...' \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```
