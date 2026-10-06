# OpenAI-compatible API

Part of the [skgate README](../README.md).

The status page opens with the two client addresses, `/v1` (this API) and `/mcp` (every included upstream), each in a box that copies on tap. Sign in with a device code on the status page.

The helper model reads as one phrase (`grok-4.7 · reasoning low`) and the aliases as a count (`1 alias`). Each is a button that opens its dialog. A signed-in card shows when the token expires, as a date and time (`2026-10-06 01:40`). skgate refreshes the token before expiry.

**Details** holds sign-in (**Refresh now**, browser sign-in, callback paste) and, under **Technical details**, the masked tokens and the sign-in endpoints. The refresh token is shown only there. Details does not edit upstream addresses. Closing a dialog with unsaved edits asks first.

Device sign-in opens the verification page in a window and shows the code, the same address as a link, and a QR of that address. The address does not include the code.

skgate proxies your SI providers behind one OpenAI-compatible address and gives their models stable names with aliases. Your Grok subscription works as this API with no API key.

Point OpenAI-compatible apps at `PUBLIC_URL` with a virtual key. `/v1`, `/api/v1`, `/api` and no prefix are equivalent: `/chat/completions` (SSE), `/models`, `/responses`, `/embeddings` and the other API paths all work with any of them.

Exception: `/messages` without a prefix is the MCP SSE bridge; use `/v1/messages`. `/mcp`, `/admin`, `/authorize`, `/token` and `/.well-known` are never affected.

```sh
export OPENAI_BASE_URL=https://skgate.example.com/v1 OPENAI_API_KEY=sk-...
```

**Model aliases.** On the provider card, the alias count opens the list. Map any name you choose to one of the account's models, for example `grok-latest` to `grok-4.7`. Alias names are letters, digits and `. _ : -`, and may not equal a real model id. Signing out removes the aliases of that provider and leaves every other provider's aliases in place.

The model list shows aliases first; a request with an alias as `model` is sent upstream with the target, streaming included; changes apply to the next request. Responses are relayed as the provider sent them, so `model` in a response names the target.

An alias whose target left the provider's list gets a warning pill with the detail on hover.

## Other providers

Grok needs no API key. **Other providers** on the status page adds the rest: OpenAI, Anthropic, Google Gemini, Mistral, DeepSeek, Groq, OpenRouter, Ollama, LM Studio, or a custom endpoint that speaks the OpenAI API.

**Add provider**, pick one (the address and a hint for the key fill in), paste the API key and **Add and test**. The address can be typed in any form; skgate finds the one that works.

skgate loads the provider's model list to test the connection; the provider is saved even when that fails, for example while a local server is down.

- The API key is encrypted at rest like the other secrets and shown masked; the key field never carries it. Leave the key empty when saving to keep the stored one. Ollama and LM Studio need no key (from a container, use an address skgate can reach, not `localhost`).
- **Details** of a key-based provider has the base URL, the key, **Test connection**, **Reload models**, and **Remove provider**, which deletes the key and its aliases. The alias count on the card opens the alias list.
- Anthropic's own API is translated: chat requests (text, images, tools, streaming) go to the Messages API and come back as OpenAI responses. Other Anthropic endpoints are not offered.
- Apps keep using one address. A request goes to the provider by its model: an alias goes to the provider and model it points at; a model listed by a ready provider goes to the first provider that lists it (Grok first); anything else goes to Grok, or to the first ready provider while Grok is signed out. The model list shows the aliases of every provider.
- Alias names are unique across providers. Adding a name that exists under another provider moves it, and the toast says so. To change the SI provider behind an app, point the alias at another provider's model.
- The MCP helper model may be any alias or model of a ready provider, so Suggest configuration works without a Grok subscription. A provider that rejects the reasoning setting is asked again without it.
- The model lists of ready providers load in the background at start. An unreachable provider never delays or fails startup.
