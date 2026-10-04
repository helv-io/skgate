# OpenAI-compatible API (Grok)

Part of the [skgate README](../README.md).

The status page opens with the two client addresses, `/v1` (this API) and `/mcp` (every included upstream), each with a copy button. Sign in with a device code on the status page. The helper model reads as one phrase (`grok-4.7 · reasoning low`) and the aliases as a count (`1 alias`). skgate refreshes the token before expiry. Tokens, endpoints, PKCE info, upstream base and fallback, the MCP helper model and aliases are in the provider's **Details** dialog. It has a section each for the model, upstream URLs, aliases and sign-in, and each section has its own **Save** that keeps edits in the other sections; closing with unsaved edits asks first. Tokens and diagnostics are under **Technical details**; the refresh token is shown only there, masked.

Point OpenAI-compatible apps at `PUBLIC_URL` with a virtual key. `/v1`, `/api/v1`, `/api` and no prefix are equivalent: `/chat/completions` (SSE), `/models`, `/responses`, `/embeddings` and the other API paths all work with any of them. `/mcp`, `/admin`, `/authorize`, `/token` and `/.well-known` are never affected.

```sh
export OPENAI_BASE_URL=https://skgate.example.com/v1 OPENAI_API_KEY=sk-...
```

**Model aliases.** In the details dialog, map a name to one of the account's models (`grok-latest` to `grok-4.7`). Alias names are letters, digits and `. _ : -`, and may not equal a real model id. The model list shows aliases first; a request with an alias as `model` is sent upstream with the target, streaming included; changes apply to the next request. Responses are relayed as the provider sent them, so `model` in a response names the target. An alias whose target left the provider's list gets a warning pill with the detail on hover.
