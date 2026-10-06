# Keys and OAuth clients

Part of the [skgate README](../README.md).

**Virtual keys** (admin: keys). The create form asks for a name; limits are optional, under **Limits**. Only a SHA-256 hash is stored; the token is shown once with a Copy button.

Send as `Authorization: Bearer sk-...` or `X-API-Key` to `/v1` and MCP endpoints. `?key=` works on MCP endpoints only, for a key that has it enabled in its edit dialog.

- **Expires** and **Last used** columns: a key that expires within 7 days is highlighted; an expired one is red. An expiration the form cannot read is marked invalid and blocks Save until it is fixed or cleared.
- **Usage** column: input and output tokens per key, abbreviated (`1.2M in / 340K out`); the tooltip has exact counts, API and MCP requests (last use is its own column). Recorded in the database (`key_usage`), written every 5 s and on shutdown, removed with the key.
  - API (`/v1`): tokens come from the provider's `usage` object, in JSON responses and in the last usage chunk of an event stream. Nothing is estimated and requests are never modified. A stream carries usage only if the client asks for it (for example `stream_options: {"include_usage": true}` on providers that need it); otherwise the call counts as a request without tokens. An aborted stream counts no tokens.
  - A request is a successful call that is not a read (model lists are not counted).
  - MCP: no tokens exist; authenticated requests made with a key are counted. OAuth access tokens are not tied to a key and are not counted.
- **edit** on a row opens one form with Save: name, max requests per minute, expiration and `?key=`. Below it, in a framed **Danger zone**, are **regenerate** and **revoke** (both ask first), then read-only details.
- **Regenerate** replaces the secret of a record (name, limits, created and last used are kept); the old token stops working immediately.
- **Revoke** disables a key. A revoked key that was never used is deleted at once; a used one is deleted 30 days after its last use. A sweep runs at startup and daily, and each purge is logged.

**OAuth clients** (admin: oauth clients). DCR clients (`/register`) and manual clients (fixed ID and secret, for clients without DCR) are listed with the host of their redirect, when they were created and their last use at `/authorize` or `/token`, most recently used first (clients never used come last).

A chip says where a client came from: **self-registered** (DCR), **created here** (the create form) or **metadata document**. The newest 500 DCR clients are kept.

**Delete unused for 30 days** removes, after a confirmation that says how many, every client last used (or, if never used, created) more than 30 days ago, with its tokens; the button is disabled when there are none.

| Client | Setup |
| --- | --- |
| Hosted MCP connectors | URL `PUBLIC_URL/mcp/<alias>`, empty client ID and secret (DCR, public, PKCE). |
| Native / CLI | DCR with a loopback redirect (`127.0.0.1`, `localhost`, `[::1]`, any port). |
| Client ID metadata document | The client sends an `https` URL as its client ID; nothing to register (see below). |
| No DCR | Manual client with its exact redirect URI. |
| Home Assistant | Steps below. |
| Scripts, mcp-proxy | Bearer key or `X-API-Key`. |
| SSE-only | `GET /sse[/alias]` then `POST /messages`, bridged to Streamable HTTP. |

Registration is forgiving: redirect URIs that cannot be used (custom schemes such as `cursor://`, plain `http` off loopback) are ignored as long as one usable URI remains, and requested grant types, response types and authentication methods that skgate does not support are dropped.

The response states what was granted. A client that asks only for unsupported things is refused.

**Client ID metadata documents.** A client may use an `https` URL with a path as its client ID. skgate fetches the JSON document there (it must repeat the URL as `client_id` and list `redirect_uris`; `client_name` is optional) and treats it as the registration. Redirect URIs are checked like registered ones.

Discovery advertises `client_id_metadata_document_supported`. The client appears in the client list with the **metadata document** chip, and the consent page shows the host of the client ID next to the name, which is the document's own claim.

Auth at `/token`:

- `token_endpoint_auth_method` omitted or `none`: public client, PKCE required (ChatGPT connectors that use `none` work this way).
- `private_key_jwt`: the document must name an `https` `jwks_uri` (and may set `token_endpoint_auth_signing_alg` to `RS256` or `ES256`). At `/token` the client sends `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer` and a JWT `client_assertion` signed with a key from that JWKS. Claims: `iss` and `sub` = the client ID URL, `aud` = `PUBLIC_URL/token`, `exp` required (lifetime at most a few minutes). skgate fetches and caches the JWKS with the same network limits as the metadata document. This is what ChatGPT's connector platform uses (`https://chatgpt.com/oauth/client.json`).

A document with a client secret, `client_secret_basic` / `client_secret_post`, or no usable redirect URI is refused.

The fetch is made by skgate on behalf of whoever opens `/authorize`, so it is restricted: only globally routable addresses (private, loopback, link-local and similar ranges are refused, checked on the address actually connected to), no redirects, no proxy, 5 seconds, 64 KB, JSON only.

A copy is kept for the `Cache-Control` max-age (between 5 minutes and 24 hours, one hour by default) and, if the document becomes unreachable, the last good copy keeps working. A failed fetch is not repeated for a minute.

The newest 500 such clients are kept. There is no setting for any of this; an instance that cannot reach the client's host simply does not accept that client ID.

Discovery: `PUBLIC_URL/.well-known/oauth-authorization-server`. Protected-resource metadata is served per endpoint (`/.well-known/oauth-protected-resource/mcp` and `/mcp/<alias>`, `resource` equal to the URL). The site-root document (`/.well-known/oauth-protected-resource`) is the same as for `/mcp`.

PKCE: a `code_challenge` that is sent must be `S256` and is always verified.

Without one, `/authorize` accepts only confidential clients (`client_secret_post`/`basic`, secret checked at `/token`) that have not used PKCE yet; after a client's first successful PKCE exchange it needs PKCE. Public and dynamically registered clients always need it.

**Home Assistant** (no DCR, sends no `code_challenge`):

1. Admin: OAuth clients, Create client, preset **Home Assistant** (redirect `https://my.home-assistant.io/redirect/oauth`, `client_secret_post`). Copy the client ID and secret.
2. Home Assistant: Settings, Devices & services, Application Credentials, add one for **Model Context Protocol** with that client ID and secret.
3. Add the Model Context Protocol integration with `https://<host>/mcp` or `https://<host>/mcp/<alias>` and approve the sign-in.

```sh
curl https://skgate.example.com/mcp/myalias -H 'Authorization: Bearer sk-...' \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```
