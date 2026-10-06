# Roadmap

What is planned for skgate's MCP client authentication (how MCP clients register with and sign in to skgate). Order within a section is rough priority. Nothing here is a promise of a date.

## Next

Small fixes to discovery.

- **Protected-resource metadata for `/sse`.** The document at `/.well-known/oauth-protected-resource/sse` already exists, but a 401 from `/sse` still points clients at the `/mcp` document. It should point at its own, like `/mcp/<alias>` does.

## Planned

- **`client_credentials` grant** for confidential clients created by an admin, for machine-to-machine use without a browser sign-in.
- **`private_key_jwt` for admin-created clients**, with the client's public key or key set registered by the admin. Clients that sign in with a metadata document URL can already use `private_key_jwt` through their `jwks_uri`.

## Later

- CORS: allow the `Mcp-Method` and `Mcp-Name` request headers.
- The stateless protocol revision of 2026-07-28.
- A token revocation endpoint.
- Alias fields in the OIDC discovery metadata.
- A short grace period for refresh token rotation, so a retry after a lost response does not sign the client out.
- Additional header names for presenting a key.
