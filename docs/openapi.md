# OpenAPI upstreams

REST APIs as MCP tools. Part of the [skgate README](../README.md); the other upstream types are in [docs/mcp.md](mcp.md).

An OpenAPI upstream turns a REST API into an MCP server. You give skgate the API's OpenAPI description, you pick the operations to offer, and every operation you pick becomes a tool. skgate itself makes the HTTP calls: there is no process to run, so it works in the `slim` image too. The upstream is served like any other: `/mcp/<alias>` (tool names as chosen), the aggregating `/mcp` (prefixed `<alias>-`), Test, keys and OAuth.

## Add one

**Add upstream**, type **OpenAPI (REST API)**.

| Field | Meaning |
| --- | --- |
| Description URL | Where the OpenAPI description is. skgate downloads it once, when you save (http or https without a user name or password, up to 10 MB, at most 5 redirects). |
| Paste the description | JSON, YAML or TOML. Pasted text wins over the URL. A description of more than a few MB is easier to give as a URL. |
| Base URL | Where the calls go. Empty takes the first server of the description (variables replaced by their defaults, a relative address resolved against the description's URL). A dropdown of the description's servers is offered; any address is accepted. |
| Outbound auth | Static credentials only: `none`, bearer token, an API key in a header, an API key in the query string, or basic (user and password). skgate sends the credential with every call and applies it last, so a model's arguments can never replace it. OAuth flows are not supported. |

OpenAPI 3.0 and 3.1 are read as they are. Swagger 2 is converted on import. The description is stored as normalized JSON; editing the upstream with an empty paste box keeps it, and a changed URL, new pasted text or **read the address again on save** replaces it. Credentials are encrypted at rest like those of other upstreams and shown masked; they are never sent to a model.

A new upstream opens its **tools page** right away.

## Choose the tools

Operations are grouped by HTTP verb (GET, POST, PUT, PATCH, DELETE, then the rest). Each group has a switch for all of its operations, each operation has its own, and a filter box narrows the list by name, path or text (the group switches then act on the rows shown). Every tool has a name and a description you can change; the defaults come from the `operationId` (or the verb and path) and the summary. When two operations share a name, the later one gets `_2`; switching one off never renames the other.

- **Reading operations (GET, HEAD) start on; everything that changes data starts off.** If the API has more than 30 reading operations, none start on and you pick.
- File uploads (multipart or binary bodies) are skipped and listed as such.
- A tool's input schema is built from the path, query and header parameters and the request body (`body`). Cookie parameters are not offered. `Authorization`, `Cookie` and similar headers are never offered either, and neither is a parameter the upstream's own credential fills in (an `X-API-Key` header or `api_key` query that the description declares and you set as the outbound auth).
- Tools carry MCP hints: `readOnlyHint` for GET and HEAD, `destructiveHint` for DELETE.

### Many tools are a cost: the counter

Models pick worse tools, and get slower and costlier, the more tools they are offered. Expose only what you need.

The tools page has a live counter that follows the switches, and the list and the Overview show the same count:

| Tools | Color | |
| --- | --- | --- |
| up to 15 | green | a good number |
| 16 to 30 | amber | a lot |
| more than 30 | red | too many |

Crossing into a worse level raises a toast, and so does switching on a whole verb group. **Nothing is ever blocked**: the counter and the notices inform you, and Save always works. The Overview page adds a row with the total of all enabled OpenAPI upstreams.

## Repair and names with the assistant

Both use the MCP helper model (the same setting as Suggest configuration) and are optional. They have prompts of their own, separate from the one that suggests an MCP server from a repository. Only the description's titles, paths, parameter names and texts go to the model; no credential does.

- **Check description** (add and edit form) reads the description and lists what is wrong: operations without an `operationId`, repeated ids, broken `$ref`s, invalid types, parameters without a name or location. A problem never stops an import; skgate uses what it can read.
- **Repair with the assistant** (shown when problems were found and a helper model is ready) asks the model for fixes, applies them to a copy and shows each change as a before and after. **Apply the fix to the pasted text** puts the repaired description in the paste box. Nothing changes until you approve it, and nothing is saved until you submit the form. Changes to servers and security are never accepted, wherever they sit in the description.
- **Suggest names** (tools page) proposes a name and a one-line description for each tool that is on, in a form that helps a model choose among tools. The suggestions fill the fields; you review them and Save.

## How calls are made

- Path values are percent-encoded; `.` and `..` are refused. Query arrays repeat the name (or join with commas when the description says so); `deepObject` is honored. Bodies are JSON, or form-encoded when the description allows only that.
- A call times out after 60 seconds. Redirects are followed up to 5 times, on the same host only and never from https to http, so a credential never follows a redirect to another host or in the clear. A query in the base URL (an `api-version`, say) is kept.
- The answer is shown as the status line and the body. At most 40,000 bytes of text go to the model; longer answers are cut with a note that says so. Binary content types are summarized, not shown. HTTP 400 and above are tool errors (`isError`).
- Errors name the cause, never the address with its credential.

## Limits

No OAuth flows for the API, no cookie parameters, no file uploads, no streaming responses, no webhooks or callbacks. The description is read when you save it; skgate never refetches it by itself. **Export JSON** leaves OpenAPI upstreams out, since an MCP client configuration cannot describe them.
