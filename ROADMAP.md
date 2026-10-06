# Roadmap

What is planned for skgate's MCP client authentication (how MCP clients register with and sign in to skgate). Order within a section is rough priority. Nothing here is a promise of a date.

## Later

- The stateless protocol revision of 2026-07-28.
- Alias fields in the OIDC discovery metadata.
- Additional header names for presenting a key.

## On request

Built only if someone asks for it in an issue. Mostly for server-to-server setups, not the usual browser sign-in.

- **`client_credentials` grant** for confidential clients created by an admin, for machine-to-machine use without a browser sign-in.
- **`private_key_jwt` for admin-created clients**, with the client's public key or key set registered by the admin. Clients that sign in with a metadata document URL can already use `private_key_jwt` through their `jwks_uri`.
