# OIDC provider hints

Part of the [skgate README](../README.md#oidc-setup).

Create a confidential client with redirect URI `PUBLIC_URL/admin/oidc/callback`, then set `OIDC_ISSUER`, `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET`.

skgate authenticates at the token endpoint with `client_secret_basic` or `client_secret_post`, whichever the provider's discovery lists (basic first), and retries once with post if basic is refused.

| Provider | Hint |
| --- | --- |
| Authelia | The client config stores a pbkdf2 hash of the secret; skgate gets the plaintext. `authelia crypto hash generate pbkdf2 --variant sha512 --random` prints both. |
| Authentik | OAuth2/OpenID provider, confidential. Add a `groups` scope mapping if you use group limits. |
| Keycloak | Client with "Client authentication" on. Add a groups mapper for group limits. |
| Zitadel, Pocket ID | Their documentation lists the same discovery, PKCE S256 and `client_secret_basic`/`client_secret_post` support. |

Optional limits: `OIDC_ALLOWED_EMAILS` and `OIDC_ALLOWED_GROUPS` (comma lists). With both empty, anyone the provider admits is admin.
