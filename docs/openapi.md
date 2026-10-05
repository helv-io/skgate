# OpenAPI upstreams

REST APIs as MCP tools. Part of the [skgate README](../README.md); the other upstream types are in [docs/mcp.md](mcp.md).

An OpenAPI upstream turns a REST API into an MCP server. You give skgate the API's OpenAPI description, you pick the operations to offer, and every operation you pick becomes a tool. skgate itself makes the HTTP calls: there is no process to run, so it works in the `slim` image too. The upstream is served like any other: `/mcp/<alias>`, the aggregating `/mcp`, Test, keys and OAuth.

## Add one

**Add upstream**, type **OpenAPI**.

| Field | Meaning |
| --- | --- |
| Address | The API, its description or its documentation page. `http://mealie:9000` is enough: skgate finds the description itself (see [Finding the description](#finding-the-description)). |
| Or paste the description | JSON, YAML or TOML. Pasted text wins over the address. A description of more than a few MB is easier to give as an address. |
| Key | The only credential field. Paste it as it is; skgate adds `Bearer` or whatever the API wants (see [The key](#the-key)). Empty means an open API. |
| Advanced | Closed by default. **Send the key as** forces a way: a header name such as `X-Custom-Key` or `Authorization`, `?name` for a query parameter, `bearer`, `basic` (the key is `user:password`) or `none`. **Base URL** overrides where the calls go. |
| Skip check | A button beside **Add** and **Save**, with a line saying what it does. It adds or saves without calling the server at all (see [The connection check](#the-connection-check)). |

The base URL is the server named by the description (variables replaced by their defaults). When the description names none, or only a relative one, it is the address the description was read from: scheme, host and port. The Advanced field only overrides this.

**Add** and **Save** are off, with the reason next to them, until the API is given. Pressing one tests the upstream first: the server must answer, and take the key. If the test fails the page stays as it is, with every field, the pasted description and the key, and a short error says why. Only a passing test stores anything.

An address on the internet needs https. Plain http is accepted for names that cannot be on the internet: a single-label name such as `mealie` (a Docker service), an IP address, `localhost`, and names ending in `.local`, `.lan`, `.internal` or `.home.arpa`. skgate decides what is on the internet with the public suffix list. The same rule applies to the address of a remote upstream, and Suggest follows it for the addresses it takes.

## Finding the description

Enter any address of the API and skgate tries, in this order and without telling you which one worked: the address as entered, any description it links, the usual places (`/openapi.json`, `/openapi.yaml`, `/openapi.yml`, `/openapi.toml`, `/swagger.json`, `/swagger.yaml`, `/swagger.yml`, `/api/openapi.json`, `/api/swagger.json`, `/v1/api-docs`, `/v2/api-docs`, `/v3/api-docs`, `/api-docs`, `/docs/openapi.json`, `/api/docs/openapi.json` and the `.yaml` and `.yml` variants), and the documentation pages (`/`, `/docs`, `/redoc`, `/swagger`, `/api/docs`) for a linked description: the `url` of Swagger UI, the `spec-url` of Redoc, or a `link` tag. The first answer whose content reads as an OpenAPI or Swagger description wins, whatever its name or content type. Only GET is sent, only to the host entered (a redirect to another host is not followed), with 6 seconds per request and 25 seconds in all. The address that worked is stored, so **Update** keeps working. If nothing is found the form says so in one sentence and you can paste the description instead.

The Suggest box on the managed form takes such an address too: it looks for the description, with no helper model, and turns the form into an OpenAPI upstream with the address and an alias filled in.

## The key

You paste one value. How it is sent comes from the description's security scheme, in OpenAPI 3 and in Swagger 2:

| The description says | skgate sends |
| --- | --- |
| `http` `bearer`, or `oauth2` | `Authorization: Bearer <key>` |
| `http` `basic` | basic auth; the key is `user:password` |
| `apiKey` in a header | that header, with the name from the description |
| `apiKey` in the query string | that query parameter, and the form warns that keys in addresses can show up in logs |
| nothing usable | `Authorization: Bearer <key>` |

Several alternatives are ranked bearer, header, basic, query. A key in the query string is only used when the description says so; it is never tried as a guess.

When the description says nothing and the server answers 401 or 403, on **Test** and on the first call, skgate quietly tries `X-API-Key`, `Api-Key`, `Authorization: Token <key>` and, for a key that looks like `user:password`, basic. It asks with safe GETs of one operation, preferably one the description marks as requiring a credential, only to the configured server. The way that works is stored and not shown; a new key forgets it. If every way is refused, Test says "the server refused the key".

### The connection check

**Add** and **Save** check the connection before anything is stored: the first GET without parameters (no required path, query or header parameter, no body), a protected one first, and among the tools that are on when any is. It never calls an operation that changes data, asks only the server of the upstream and keeps the http and https rule. The key is never logged.

| Answer | Result |
| --- | --- |
| 2xx | Saved. |
| 401 or 403 | Not saved: "the server refused the key", or "the server needs a key" when none was entered. A 403 first tries up to two more reads, in case that one operation is only for admins. |
| 404 | Not saved: "not found, check the base URL". |
| Another status, no answer or a timeout | Not saved, with a short error. |

A failed check returns to the same form with every field kept. **Skip check** is for when the server is down, behind a firewall that skgate cannot reach, or should not be called yet: it stores the upstream and never calls the server. A description with no such GET has no check, so it shows no Skip check button. If the check fails, the error says why and **Skip check** is still there. An edit checks only when the key, the base URL, the description or the switch to enabled changed. **Test** uses the same call.

Upstreams saved before this kept their kind (bearer, header, query, basic) and behave exactly as before; their Advanced line shows it. The key is encrypted at rest, masked in the UI, and never sent to a model.

OpenAPI 3.0 and 3.1 are read as they are. Swagger 2 is converted on import. The description is stored as normalized JSON; editing the upstream with an empty paste box keeps it, and a changed URL or new pasted text replaces it, and **Update** on the tools page reads the address again (see [Update from the address](#update-from-the-address)). Credentials are encrypted at rest like those of other upstreams and shown masked; they are never sent to a model.

A new upstream opens its **tools page** right away.

## Choose the tools

Operations are grouped by HTTP verb (GET, POST, PUT, PATCH, DELETE, then the rest). Each group has a switch for all of its operations, each operation has its own, and a filter box narrows the list by name, path or text (the group switches then act on the rows shown). Every tool has a name and a description you can change; the defaults come from the `operationId` (or the verb and path) and the summary. When two operations share a name, the later one gets `_2`; switching one off never renames the other.

- **Reading operations (GET, HEAD) start on; everything that changes data starts off.** If the API has more than 30 reading operations, none start on and you pick.
- File uploads (multipart or binary bodies) are skipped and listed as such.
- A tool's input schema is built from the path, query and header parameters and the request body (`body`). Cookie parameters are not offered. `Authorization`, `Cookie` and similar headers are never offered either, and neither is a parameter the upstream's own credential fills in (an `X-API-Key` header or `api_key` query that the description declares and you set as the outbound auth).
- Tools carry MCP hints: `readOnlyHint` for GET and HEAD, `destructiveHint` for DELETE.

### Many tools are a cost: the counter

The tools page has a live counter that follows the switches; the upstream list shows each upstream's count and the Overview the total. The count is colored (green up to 15, amber to 30, red above); the color alone is the signal, with no judgmental words. Switching on a whole verb group raises a toast. **Nothing is ever blocked**: Save always works. The Overview page adds a row with the total of all enabled OpenAPI upstreams.

## Test a tool

On the tools page every tool that is on has a **Test** button (the same control appears on a remote or managed upstream's Test page). It opens a form made from the tool's input schema: a labelled field per argument (text, number, a checkbox for true or false, a list for fixed choices, a small JSON box for objects and lists), required ones marked "required", the description as one short line, and defaults filled in. **Edit as JSON** shows the raw arguments and back. **Run** calls the upstream for real, as the admin, through the same code a client's tool call uses (the same key, the same limits), and shows the answer as indented JSON or a plain error with the time it took. **Edit inputs** goes back with everything kept; **Close** leaves. Only tools that are on and saved can be tested, and nothing is stored by testing. It needs the admin session like every admin page.

## Repair and names

Both use the MCP helper model, the same setting as Suggest configuration, and are optional. Only titles, paths, parameter names and texts go to the model, never a credential.

- **Check** lists what is wrong: operations without an `operationId`, repeated ids, broken `$ref`s, invalid types, parameters without a name or location. A problem never stops an import.
- **Repair** (with a helper model, when problems were found) proposes fixes as a before and after. **Apply** puts the repaired text in the paste box and checks it again. Nothing is saved until you submit the form. Changes to servers and security are never accepted.
- **Suggest names and selection** (tools page) names every tool and ticks a small core set (about 15, at most 30). You can switch more on, then Save. The button is grayed with the same reason and MCP helper model control as Suggest configuration when the helper cannot run. While it runs the button stays off.

## Update from the address

Updates are manual. Nothing polls the address, nothing refreshes by itself, and nothing touches a running gateway until you confirm.

On the tools page, **Update** reads the description again from its address. It is on only for an upstream that was added with an address. A pasted description has no address: the button is off, with "no address to update from" as its tooltip and as a line of text next to it.

1. **Update** downloads the description again and, if the text is the same as last time, says "No changes" and stops.
2. Otherwise the new description gets the same checks and repair as **Check**. With no helper model the repair is skipped and the screen says so.
3. A review screen shows the tools exposed now and after, the operations added, removed and changed, and what the repair fixed.
4. **Confirm update** replaces the stored description. **Cancel** leaves everything as it was.

Your tool selection (which tools are on, their names and descriptions) is kept for every operation that still exists. New operations stay off. The tools page shows when the description was last read ("Updated ...").

## How calls are made

- Path values are percent-encoded; `.` and `..` are refused. Query arrays repeat the name (or join with commas when the description says so); `deepObject` is honored. Bodies are JSON, or form-encoded when the description allows only that.
- A call times out after 60 seconds. Redirects are followed up to 5 times, on the same host only and never from https to http, so a credential never follows a redirect to another host or in the clear. A query in the base URL (an `api-version`, say) is kept.
- The answer is shown as the status line and the body. At most 40,000 bytes of text go to the model; longer answers are cut with a note that says so. Binary content types are summarized, not shown. HTTP 400 and above are tool errors (`isError`).
- Errors name the cause, never the address with its credential.

## Limits

No OAuth flows for the API, no cookie parameters, no file uploads, no streaming responses, no webhooks or callbacks. The description is read when you save it or press **Update**; skgate never refetches it by itself. **Export JSON** leaves OpenAPI upstreams out, since an MCP client configuration cannot describe them.
