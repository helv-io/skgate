# Changelog

All notable changes to skgate are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file is written by `tools/changelog`: when a version tag is pushed, a workflow adds that version's section and commits it to master. See docs/development.md.

## [Unreleased]

### Added

- README: Star History chart
- README: model aliases are opened from the provider card

### Changed

- One-click actions save in place on every admin page: the upstream switches, Delete and Detect, the process actions, a key's Save and Revoke, client deletes, and adding or removing a provider. The page does not reload, lose its scroll or move a control, and only an error shows a toast
- An SI helper's status shows on its own line under its buttons, with its room kept, so no button moves when it appears or changes
- Phones do not zoom. Pinch and double-tap zoom are off, and fields are 16px so iOS does not zoom in on focus
- A provider other than Grok has a prefix, and its models are named prefix_model
- The update pill updates the upstream in place, and the row refreshes where it is
- A helper request keeps its button and shows the stage beside it. An error turns that line red
- Leaving while a request runs, or with unsaved changes, asks first
- A helper from another provider is shown as its model id
- /sse answers 401 with its own protected-resource metadata, and a token for that resource is accepted there
- Browsers may send the Mcp-Method and Mcp-Name headers
- A rotated refresh token still works once, for about a minute, so a lost response can be retried
- POST /revoke revokes an access or refresh token, and discovery advertises revocation_endpoint
- An alias target and a helper-model field save when they change, and the status page updates without a reload
- Provider details is the helper model, the aliases, and remove. The token refreshes in the background
- The helper model waits 300 seconds by default. A timeout already saved is kept
- A saved secret shows in its field as the placeholder. Leaving the field empty keeps it
- On OAuth clients, Create client sits above the list, and Delete unused stays directly above the list
- The alias name in the Model aliases table opens that provider's dialog, and Delete removes the alias
- Device sign-in opens the verification address with the user code in the window, the link and the QR
- Sign out of a provider removes its aliases and leaves the others
- Device sign-in shows the code, the verification address, and a QR
- The helper-model and alias counts open their own dialogs
- A provider card shows when the token expires, as a date and time

### Fixed

- On the tools page, a change to the MCP helper model, reasoning or timeout stays and is saved
- The update pill updates the upstream when tapped or clicked. A failed update shows as a toast

### Removed

- The per-row Save on an alias, and Save, Reload models and the unsaved mark on the helper model
- Technical details, manual refresh and sign-in controls from provider details
- Sign out on the provider card
- Provider details no longer edit upstream URLs

## [0.23.3] - 2026-10-06

### Changed

- An SI helper's status shows on its own line under its buttons, with its room kept, so no button moves when it appears or changes

## [0.23.2] - 2026-10-06

### Changed

- Phones do not zoom, and a change to the MCP helper model, reasoning or timeout on the tools page stays and is saved

## [0.23.1] - 2026-10-06

### Fixed

- The update pill updates the upstream when tapped or clicked, and a failed update shows as a toast

## [0.23.0] - 2026-10-05

### Changed

- A provider other than Grok has a prefix, and its models are named prefix_model

## [0.22.3] - 2026-10-05

### Changed

- Helper requests show their stage, /sse has its own metadata, and refresh tokens can be revoked

## [0.22.2] - 2026-10-05

### Changed

- Alias and helper fields save on change, provider details is the helper model and aliases, and a saved secret is the field placeholder

## [0.22.1] - 2026-10-05

### Changed

- Device sign-in opens the verification address with the user code in the window, the link and the QR

## [0.22.0] - 2026-10-05

### Changed

- Provider cards open the helper and alias dialogs, device sign-in shows a code and QR, sign-out removes that provider's aliases, details no longer edit upstream URLs, and the token row says when it expires

## [0.21.7] - 2026-10-05

### Changed

- Show the test-page auth scheme once (#7)

### Fixed

- Test buttons on the tools list line up in their own column

### Removed

- Drop Sign in again from the signed-in Grok card (#8)

## [0.21.6] - 2026-10-05

### Changed

- Maintenance only

## [0.21.5] - 2026-10-05

### Changed

- Split long README and docs notes into short paragraphs (#4)

## [0.21.4] - 2026-10-05

### Changed

- Mark Open WebUI as a tested OK MCP client (#3)

## [0.21.3] - 2026-10-05

### Changed

- Sort Comparison providers and mark free tiers (#2)

## [0.21.2] - 2026-10-05

### Changed

- List tested MCP clients and release 0.21.2 (#1)

## [0.21.1] - 2026-10-05

### Removed

- Drop alias-tool naming fluff from UI and docs

## [0.21.0] - 2026-10-05

### Changed

- Serve / as aggregate MCP for clients; root PRM same as /mcp

## [0.20.0] - 2026-10-05

### Changed

- CIMD private_key_jwt; drop One upstream copy row

## [0.19.5] - 2026-10-05

### Changed

- Maintenance only

## [0.19.4] - 2026-10-05

### Changed

- Maintenance only

## [0.19.3] - 2026-10-05

### Changed

- Maintenance only

## [0.19.2] - 2026-10-05

### Changed

- Maintenance only

## [0.19.1] - 2026-10-05

### Changed

- Maintenance only

## [0.19.0] - 2026-10-05

### Changed

- Suggest names and selection, MCP Test tool try, quieter counts

## [0.18.0] - 2026-10-05

### Changed

- Suggest names: Selected or All, with no tool cap

## [0.17.0] - 2026-10-04

### Added

- Test a tool from the tools page: a form made from the tool's input schema, Run, and the answer, through the same code a client's call uses.

### Changed

- The OpenAPI form has a Skip check button that adds the upstream without calling the server, and shorter wording across the add, edit and test screens.

## [0.16.1] - 2026-10-04

### Changed

- The connection check line on the OpenAPI form is shorter.

## [0.16.0] - 2026-10-04

### Added

- Add and Save on an OpenAPI upstream check the connection with the first GET tool that needs no parameters, protected ones first. A server that needs a key or refuses the key is not saved, and the form keeps everything you entered.
- Skip check on the OpenAPI form saves without the check.

### Changed

- An edit checks the connection only when the key, the base URL, the description or the enabled switch changed.
- Test on an OpenAPI upstream fails with "the server needs a key" when no key is set and the server asks for one, instead of passing with a warning.

## [0.15.2] - 2026-10-04

### Changed

- Admin hints are shorter and drop jargon the screens do not need.

## [0.15.1] - 2026-10-04

### Changed

- The OpenAPI add, edit, tool selection and update screens and their error messages use shorter, plainer wording. Hints that repeated a label are gone.

## [0.15.0] - 2026-10-04

### Added

- The OpenAPI form finds the description itself. Enter the address of the API, such as http://mealie:9000, and skgate tries the usual places, reads linked descriptions, and saves the address that worked.
- The server address of an OpenAPI upstream comes from the description, or from the address you gave. The Base URL in Advanced is only an override.
- One key field for an OpenAPI upstream. skgate sends it the way the description says, bearer, basic, a header or the web address, and tries the usual alternatives when the server refuses it. A line in Advanced sets the way by hand.
- The source box of Suggest takes the address of a REST API and turns the form into an OpenAPI upstream.
- Add and Save test the upstream first and save only when the test passes. A failed add or save keeps the form as it was, with every field and a short error.
- The Add and Save buttons stay disabled until the required fields are filled and say what is missing.

### Changed

- One rule for addresses: a server on the internet needs https; plain http stays fine for names like mealie, IP addresses and .local names. It applies to OpenAPI, remote MCP servers, import and the tester.
- Test on an OpenAPI upstream sends one safe read to check the key and the server.

## [0.14.2] - 2026-10-04

### Fixed

- Tool descriptions on the Test page no longer look clickable when a click shows nothing: text that fits is plain, and only a line that is cut off gets the dotted underline and opens on a click or Enter

### Changed

- The Test page's tools table is split about 30% Name and 70% Description on wide screens; long names wrap and phones keep the card layout

## [0.14.1] - 2026-10-04

### Changed

- The OpenAPI paste box and the MCP import box use CodeMirror 6, a maintained editor bundled into skgate, instead of our own code. Same features: colours for JSON, YAML and TOML, matching brackets, an underline on syntax errors (TOML now checked too), and auto-indent
- The paste box keeps working without script, and the page's content security policy is unchanged
- Third-party licence notices are in THIRD_PARTY_NOTICES.md

## [0.14.0] - 2026-10-04

### Added

- The OpenAPI paste box (JSON, YAML or TOML) and the MCP import box (JSON) show syntax colours, mark the matching bracket, point at the first syntax error with its line, and indent new lines for you
- A line under the OpenAPI paste box says what the text reads as, or where it goes wrong

### Changed

- The boxes work well on phones: 16 px text, they scroll inside instead of the page, and the phone keyboard's Enter indents too
- Colours stop for very long text (over 200 000 characters); the text stays readable

## [0.13.0] - 2026-10-04

### Added

- OpenAPI upstreams: an Update button on the tools page reads the description again from its address, runs it through the SI layer, and shows what changed (tool count, added, removed and changed operations). Nothing is replaced until you confirm; Cancel leaves everything as it was
- A pasted description has the Update button off, with the reason shown next to it
- "Updated <time>" or "unchanged" on the tools page, from the time and hash stored with each OpenAPI description

### Changed

- The "read the address again" checkbox on the edit form is replaced by the Update button
- Tool selection is kept for operations that still exist; new operations start off

## [0.12.8] - 2026-10-04

### Changed

- SI wording covers only text that is ours (screens, docs, descriptions, tooltips); names, keys, file names and links are unchanged

## [0.12.7] - 2026-10-04

### Changed

- README is MCP-first; a new section covers proxying SI providers, model aliases and using a Grok subscription as an OpenAI-compatible API with no API key
- One description of skgate in the registry, Docker labels and Unraid template
- SI wording in docs and screens; shorter /mcp note in operations.md

## [0.12.6] - 2026-10-04

### Changed

- Style pass over the admin UI: buttons all start with a capital, one summary style, shared colour tokens, empty tables say None
- Status pills and the tool count are text and colour only (no icon shapes)
- Copy boxes keep working as in v0.12.5

## [0.12.5] - 2026-10-04

### Added

- Add provider: the address can be typed in any form and skgate finds the one that works; plain error messages; shorter texts

### Changed

- One copy box for every address, client ID and secret, new key and sign-in code: the whole box copies when tapped, with a hint and a toast
- No icon glyphs in the UI (More, disclosure and warning are text now)

## [0.12.4] - 2026-10-04

### Changed

- Docs corrected against the code: MCP, operations, API, OpenAPI, forward-auth paths, alias form labels, and PUID/PGID (must not be 65534)

### Fixed

- Stopping the server now also stops managed MCP servers cleanly (with a test)

## [0.12.3] - 2026-10-04

### Changed

- Browser tests now run in CI; log times go through one formatter; tests that could not fail now check what they say; the managed-process tests stop everything before cleanup

### Fixed

- A refused write is no longer reported as saved (provider, alias, upstream and client changes)
- Migrations run in one transaction; a failed legacy-env migration is retried; the OpenAPI cache no longer keeps a stale read
- Long names wrap and no table scrolls sideways inside its card
- Disabled buttons are dimmed, a failing upstream's error is only in the hover text, a failed save says why, and plain forms cannot be double-submitted

### Security

- Go-jose 4.1.4 (GO-2026-4865) and Go 1.25 for the images and CI; govulncheck runs in CI and finds nothing
- The token endpoint refuses a `resource` wider than the one granted
- Admin sign-out revokes the session and is a POST; the session secret is read once

## [0.12.1] - 2026-10-04

### Changed

- Docs, README, registry and Unraid text corrected to match the code

### Fixed

- Tool names stay put when another tool is switched off. The test page of an OpenAPI upstream shows only what is true
- An oversized form now says so. The saved-key error names SECRETS_KEY
- Real security schemes are no longer reported as problems. A description under 10 MB is no longer refused after it is normalized. Repair can never touch servers or security anywhere in the description

### Security

- A description whose references fan out can no longer use all of the gateway's memory: schemas are inlined under a budget
- A parameter named like the upstream's own credential is no longer offered to the model, and no longer makes a tool uncallable
- A credential no longer follows a redirect from https to http. A query in the base URL is kept. Long non-UTF-8 answers are no longer cut at the first bad byte
- Suggest refuses a token in a repository address and no longer sends it to the model

## [0.12.0] - 2026-10-04

### Added

- Add an upstream of type OpenAPI with a URL or pasted JSON, YAML or TOML. Swagger 2 is converted

### Changed

- skgate makes the HTTP calls itself, so no process is needed and it works in the slim image
- Pick which operations become tools. Reads start on, writes start off. The tool count is shown as good (up to 15), a lot (up to 30) or too many, and nothing is blocked
- Optional check and repair of the description, and name suggestions, with the helper model. You approve every change
- Static credentials: bearer, header, query or basic. They are sealed at rest and never sent to a model

## [0.11.0] - 2026-10-04

### Added

- Add OpenAI, Anthropic, Google Gemini, Mistral, DeepSeek, Groq, OpenRouter, Ollama, LM Studio or any OpenAI-compatible endpoint from the status page. Pick a preset, paste the API key, and skgate tests the connection

### Changed

- API keys are encrypted at rest and shown masked. Local servers need no key
- One address for your apps: a request goes to the provider that owns the model, and an alias is now a provider plus a model, so you switch an app's SI provider in one place
- Anthropic is translated to the OpenAI API (text, images, tools, streaming)
- The MCP helper model can be a model of any ready provider, so Suggest configuration works without Grok
- Starting skgate never waits for a provider: model lists load in the background
- The default branch is now master; CI runs on it

## [0.10.1] - 2026-10-04

### Changed

- Process page: a live output viewer. It follows new lines while the page is open, scrolls back without jumping, and offers "jump to latest"
- Filter by text or /regex/, by level, or stderr only; times as a clock or "5 s ago"; copy the shown lines or download the whole output
- Stdout, stderr and skgate's own lines (starting, started, stopped, exited) are tinted and named, with a divider at each run start
- Each process keeps its current and previous run only, at most LOG_LINES lines (default 1000) or 512 KB. The log survives a restart of skgate
- Unraid Community Apps template and profile files added

### Security

- Values of secret environment variables and the git token are masked before a line is stored

## [0.10.0] - 2026-10-03

### Changed

- OAuth clients: created and last-used columns, plain source chips (self-registered, created here, metadata document), the redirect host, and a button that deletes clients unused for 30 days
- Status: an Overview with the /v1 and /mcp addresses and copy buttons, and readable pills ("grok-4.7 · reasoning low", "1 alias")
- Process page: buttons grouped into lifecycle (only what applies), maintenance, go to, and danger
- Roadmap: v0.11.0 adds more SI providers

## [0.9.3] - 2026-10-03

### Changed

- Keyboard focus is visible on every control, including the key form's rate and expiry fields and the quick buttons
- Phones: every control is at least 44px; upstream names have a 44px tap area; endpoint URLs wrap at slashes
- Upstream toggles are switches; "not in /mcp" is neutral and an unavailable switch is dimmed
- Key dialog: regenerate and revoke are in a Danger zone below Save
- Suggest results: warnings first with an icon, notes folded
- Test screen: summary line, buttons first, tool filter, one-line descriptions
- Import checks the JSON while you type and blocks Import until it is valid

## [0.9.2] - 2026-10-03

### Changed

- The release workflow now lists each stable tag in the official MCP registry (GitHub OIDC, no secret)
- README: the aggregated `/mcp` leaves on-demand servers out

## [0.9.1] - 2026-10-03

### Changed

- Quickstart: `docker-compose.quickstart.yml` runs skgate with a bundled OIDC provider (Dex) and one fixed login, with no accounts needed. See `docs/quickstart.md`. For a local trial only
- Both images carry the `io.modelcontextprotocol.server.name` label, and `server.json` and `glama.json` are in the repo for the MCP registry and Glama listings
- README: a note that skgate is built with SI assistance

## [0.9.0] - 2026-10-03

### Changed

- Grok details dialog in sections (model, upstream URLs, aliases, sign-in). Each section saves on its own without reloading away edits in the others, and closing with unsaved edits asks first. The model picker is grouped, the alias row is labelled, tokens and diagnostics are under Technical details, and Refresh now and Browser sign-in sit in one row
- Upstream list: Add upstream opens its own page. Each row keeps Test and Copy URL; Details, Edit and Delete are in a ⋯ menu. Status and Type are separate columns (`remote`, `managed · git`, `managed · npm`, ...). Import and export are above the table, endpoint URLs at the top, and phone cards are short
- The top bar is sticky on every page. On phones it is one row with a Menu button

## [0.8.5] - 2026-10-03

### Changed

- Labels that name their fields; danger buttons are red at rest
- An explicit Sign out button and a secondary Sign in again
- The Suggest flow is reordered, disabled without a source, and says each error once
- 15 px text and a 1200 px content width; Expires and Last used columns in the keys list; Save is blocked for an unreadable expiration

### Fixed

- Fixes from a UX review: alias pattern, no outbound auth row for managed upstreams, heading alias case, expiry quick buttons, stable scrollbar, mobile header, rounded latency, branded 404 and authorize error pages

## [0.8.4] - 2026-10-03

### Added

- New logo and social card

### Changed

- A simpler virtual keys screen: the create form is a name and a button with optional Limits, one edit dialog with a single Save (name, rate limit, expiry, `?key=`), regenerate and revoke inside it, and keys can be renamed
- Toasts are always in front of everything, also over dialogs

## [0.8.3] - 2026-10-03

### Changed

- Discovery advertises `client_id_metadata_document_supported`

### Security

- An https client ID is resolved through its client ID metadata document, fetched with a restricted fetcher (public addresses only, no redirects, size and time limits, cached)

## [0.8.2] - 2026-10-03

### Changed

- The Test button is on every upstream row; remote upstreams show their health and last error in the list

### Fixed

- Test explains refused connections and unresolvable names and names the port that answers MCP. Upstream errors no longer carry addresses
- Dynamic client registration ignores unusable redirect URIs and reduces grants and auth methods to what skgate supports

## [0.8.1] - 2026-10-03

### Changed

- The form remembers which variables are secret
- Suggest flags each variable as secret and required and never sends a value

### Fixed

- Managed servers no longer receive environment variables with an empty value

## [0.8.0] - 2026-10-03

### Changed

- Optional `GITHUB_TOKEN` for Suggest configuration when it reads GitHub repositories. An upstream's own access token takes precedence
- A clear message when GitHub's rate limit is reached
- Faster CI and release builds (native amd64 and arm64 builders)

## [0.7.11] - 2026-10-03

### Changed

- The helper model picker lists the aliases defined in skgate; they are resolved when the helper calls the provider
- A reasoning pill (auto, low, medium, high) sits next to the helper model
- The signed-in pill on the status page is also the Sign out button

## [0.7.10] - 2026-10-03

### Changed

- The `?key=` switch moved from a global setting into each key's Details dialog. It is off by default; existing keys keep working if the old setting was on
- Keys that may be sent as `?key=` get a subtle tint in the list

## [0.7.9] - 2026-10-03

### Changed

- Keys list: a Usage tooltip shows when a key was last used
- Actions column headers; Status comes first in every list
- The hard stop for keys is replaced by a typed expiration date
- Clicking outside a dialog closes it only when it just shows information
- The version in the header links to the project and hints at an available update

## [0.7.8] - 2026-10-03

### Changed

- Dialogs follow the URL fragment: closing clears it, Back closes, clients are addressed by id
- Helper picker lists the provider's own model aliases next to their models
- Details dialogs: what can be changed first, read-only information last
- Reasoning replaces Effort in the helper picker; the chat setting is removed
- Admin screens are GET and refreshable; upstream routes carry the alias
- Helper model picker: editable timeout, a note on slow models, a 600 s hint
- Helper model timeout setting and a name-based guess for heavy models
- Report why a process died when our message hit a closed pipe
- Approve is the filled primary button on every screen width

## [0.7.7] - 2026-10-03

### Changed

- Consent screen and the other standalone screens: one centered card
- List tables become cards on phones; Details dialogs hold the rest
- Copy buttons take a value and answer with a toast; toasts show above dialogs
- Log at start when every OIDC user is an admin

## [0.7.6] - 2026-10-03

### Changed

- /mcp includes only always-on managed servers; on-demand ones are excluded automatically
- Store: GetSecret, SetSecret and an idempotent step that seals the Grok token settings at open
- Admin: set key limits when creating a key and in a per-key dialog
- Optional per-key limits on /v1: requests per minute and a hard stop

### Security

- Refuse to run skgate as nobody or nogroup; keep the database files private
- Seal the Grok tokens at rest with the secrets box
- Require OAuth consent by default
- Run managed servers as nobody, each with its own writable directories

## [0.7.5] - 2026-10-03

### Added

- Add a stored reasoning effort for proxied chat requests, retried without when rejected
- Add the Go toolchain to the full image and a go runner
- Add the .NET 10 SDK to the full image
- Support .NET repositories: dotnet runner, project detection, trimmed README

### Changed

- Gofmt
- Suggest: visible timed-out state with stage, elapsed time and a Lower effort button; stream activity
- Effort dropdown under the model dropdown in the shared model picker, separate chat effort setting
- Suggest: stream the model call, selectable reasoning effort, idle and overall timeouts

### Fixed

- NuGet refusal no longer claims the image lacks dotnet

## [0.7.4] - 2026-10-02

### Fixed

- Restore the inline-form script that the Suggest rewrite cut off

## [0.7.3] - 2026-10-02

### Changed

- Suggest configuration: live stages, source summary and staggered variable rows

## [0.7.2] - 2026-10-02

### Changed

- Model picker: show the load time before the Reload models button

## [0.7.1] - 2026-10-02

### Changed

- Honor TZ: embed the zone database and format displayed times through one helper
- OIDC: pick client_secret_basic or client_secret_post from discovery, retry post on invalid_client, log the method
- Log refused admin actions and Grok sign-in and refresh failures
- Log every Suggest configuration stage and say why it failed
- Show toasts at the bottom right

## [0.7.0] - 2026-10-02

### Changed

- Default the add form to the managed type when Suggest can run
- Pick the MCP helper model from the upstream page
- Rename "helper model" to "MCP helper model" in the UI and docs
- Use ./data as the data volume in examples

## [0.6.2] - 2026-10-02

### Added

- First tagged version: an OAuth-protected MCP gateway that proxies remote MCP servers and runs managed ones, with Grok sign-in and an OpenAI-compatible API

### Changed

- Requires Go 1.24

[Unreleased]: https://github.com/helv-io/skgate/compare/v0.23.3...HEAD
[0.23.3]: https://github.com/helv-io/skgate/compare/v0.23.2...v0.23.3
[0.23.2]: https://github.com/helv-io/skgate/compare/v0.23.1...v0.23.2
[0.23.1]: https://github.com/helv-io/skgate/compare/v0.23.0...v0.23.1
[0.23.0]: https://github.com/helv-io/skgate/compare/v0.22.3...v0.23.0
[0.22.3]: https://github.com/helv-io/skgate/compare/v0.22.2...v0.22.3
[0.22.2]: https://github.com/helv-io/skgate/compare/v0.22.1...v0.22.2
[0.22.1]: https://github.com/helv-io/skgate/compare/v0.22.0...v0.22.1
[0.22.0]: https://github.com/helv-io/skgate/compare/v0.21.7...v0.22.0
[0.21.7]: https://github.com/helv-io/skgate/compare/v0.21.6...v0.21.7
[0.21.6]: https://github.com/helv-io/skgate/compare/v0.21.5...v0.21.6
[0.21.5]: https://github.com/helv-io/skgate/compare/v0.21.4...v0.21.5
[0.21.4]: https://github.com/helv-io/skgate/compare/v0.21.3...v0.21.4
[0.21.3]: https://github.com/helv-io/skgate/compare/v0.21.2...v0.21.3
[0.21.2]: https://github.com/helv-io/skgate/compare/v0.21.1...v0.21.2
[0.21.1]: https://github.com/helv-io/skgate/compare/v0.21.0...v0.21.1
[0.21.0]: https://github.com/helv-io/skgate/compare/v0.20.0...v0.21.0
[0.20.0]: https://github.com/helv-io/skgate/compare/v0.19.5...v0.20.0
[0.19.5]: https://github.com/helv-io/skgate/compare/v0.19.4...v0.19.5
[0.19.4]: https://github.com/helv-io/skgate/compare/v0.19.3...v0.19.4
[0.19.3]: https://github.com/helv-io/skgate/compare/v0.19.2...v0.19.3
[0.19.2]: https://github.com/helv-io/skgate/compare/v0.19.1...v0.19.2
[0.19.1]: https://github.com/helv-io/skgate/compare/v0.19.0...v0.19.1
[0.19.0]: https://github.com/helv-io/skgate/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/helv-io/skgate/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/helv-io/skgate/compare/v0.16.1...v0.17.0
[0.16.1]: https://github.com/helv-io/skgate/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/helv-io/skgate/compare/v0.15.2...v0.16.0
[0.15.2]: https://github.com/helv-io/skgate/compare/v0.15.1...v0.15.2
[0.15.1]: https://github.com/helv-io/skgate/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/helv-io/skgate/compare/v0.14.2...v0.15.0
[0.14.2]: https://github.com/helv-io/skgate/compare/v0.14.1...v0.14.2
[0.14.1]: https://github.com/helv-io/skgate/compare/v0.14.0...v0.14.1
[0.14.0]: https://github.com/helv-io/skgate/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/helv-io/skgate/compare/v0.12.8...v0.13.0
[0.12.8]: https://github.com/helv-io/skgate/compare/v0.12.7...v0.12.8
[0.12.7]: https://github.com/helv-io/skgate/compare/v0.12.6...v0.12.7
[0.12.6]: https://github.com/helv-io/skgate/compare/v0.12.5...v0.12.6
[0.12.5]: https://github.com/helv-io/skgate/compare/v0.12.4...v0.12.5
[0.12.4]: https://github.com/helv-io/skgate/compare/v0.12.3...v0.12.4
[0.12.3]: https://github.com/helv-io/skgate/compare/v0.12.1...v0.12.3
[0.12.1]: https://github.com/helv-io/skgate/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/helv-io/skgate/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/helv-io/skgate/compare/v0.10.1...v0.11.0
[0.10.1]: https://github.com/helv-io/skgate/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/helv-io/skgate/compare/v0.9.3...v0.10.0
[0.9.3]: https://github.com/helv-io/skgate/compare/v0.9.2...v0.9.3
[0.9.2]: https://github.com/helv-io/skgate/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/helv-io/skgate/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/helv-io/skgate/compare/v0.8.5...v0.9.0
[0.8.5]: https://github.com/helv-io/skgate/compare/v0.8.4...v0.8.5
[0.8.4]: https://github.com/helv-io/skgate/compare/v0.8.3...v0.8.4
[0.8.3]: https://github.com/helv-io/skgate/compare/v0.8.2...v0.8.3
[0.8.2]: https://github.com/helv-io/skgate/compare/v0.8.1...v0.8.2
[0.8.1]: https://github.com/helv-io/skgate/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/helv-io/skgate/compare/v0.7.11...v0.8.0
[0.7.11]: https://github.com/helv-io/skgate/compare/v0.7.10...v0.7.11
[0.7.10]: https://github.com/helv-io/skgate/compare/v0.7.9...v0.7.10
[0.7.9]: https://github.com/helv-io/skgate/compare/v0.7.8...v0.7.9
[0.7.8]: https://github.com/helv-io/skgate/compare/v0.7.7...v0.7.8
[0.7.7]: https://github.com/helv-io/skgate/compare/v0.7.6...v0.7.7
[0.7.6]: https://github.com/helv-io/skgate/compare/v0.7.5...v0.7.6
[0.7.5]: https://github.com/helv-io/skgate/compare/v0.7.4...v0.7.5
[0.7.4]: https://github.com/helv-io/skgate/compare/v0.7.3...v0.7.4
[0.7.3]: https://github.com/helv-io/skgate/compare/v0.7.2...v0.7.3
[0.7.2]: https://github.com/helv-io/skgate/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/helv-io/skgate/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/helv-io/skgate/compare/v0.6.2...v0.7.0
[0.6.2]: https://github.com/helv-io/skgate/releases/tag/v0.6.2
