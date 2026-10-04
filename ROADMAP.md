# Roadmap

What is planned for skgate: MCP client authentication (how MCP clients register with and sign in to skgate) and the AI API. Order within a section is rough priority. Nothing here is a promise of a date.

## Next

Small fixes to discovery.

- **Protected-resource metadata for every endpoint.** `/sse` gets its own protected-resource document like `/mcp` and `/mcp/<alias>`, and the `resource` value is consistent with and without a trailing slash.

## Planned

- **`client_credentials` grant** for confidential clients created by an admin, for machine-to-machine use without a browser sign-in.
- **`private_key_jwt`** client authentication, with the client's public key or key set registered by the admin.

## v0.11.0: more AI providers

Grok stays first (your subscription, no API key). Other providers are added next to it, for the helper model and for the `/v1` API alike.

- A provider list of presets kept as data (name, base URL, key hint, docs link): OpenAI, Anthropic (its own adapter), Gemini, Mistral, DeepSeek, Groq, OpenRouter, Ollama and LM Studio (no key), and Custom (any OpenAI-compatible base URL).
- A masked API key per provider, encrypted at rest like upstream secrets; a model picker filled from the provider's model list; a Test connection button.
- Aliases point at a provider and a model.

## Later

- CORS: allow the `Mcp-Method` and `Mcp-Name` request headers.
- The stateless protocol revision of 2026-07-28.
- A token revocation endpoint.
- Alias fields in the OIDC discovery metadata.
- A short grace period for refresh token rotation, so a retry after a lost response does not sign the client out.
- Additional header names for presenting a key.
