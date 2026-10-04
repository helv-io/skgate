# AGENT.md

Guidelines for changes to skgate. Applies to code, UI, docs and releases.

## Product principle

skgate must work out of the box. Users are not security experts.

What skgate is, in one wording used everywhere it is described (README, registry, Docker labels, Unraid): an OAuth-protected MCP gateway that can also run the servers; it also proxies SI providers behind one OpenAI-compatible address with model aliases, and a Grok subscription becomes that API with no API key. The lead is always MCP. Claims stay modest: no "only" and no "first".

- Prefer secure defaults that adapt automatically over config toggles. Example: PKCE is used if the client sends it, and is then required for that client.
- Minimize per-client and per-upstream settings. Every new option needs a reason a default cannot cover.
- Compatibility first, without weakening security for public clients.
- Apply this to every design decision.

## Style

- Technical audience. No fluff, no hand-holding, short lines.
- Generic examples. Name Authentik next to Authelia, never one alone. No house-specific names in code, docs or examples.
- Provider-neutral. No provider-specific wording or defaults outside that provider's own definition.
- Consistent labels, casing and spacing. One version display: semver (`vX.Y.Z`), in the header only.
- No URL-variant or trailing-slash labels. Correct stored data silently.

## UI

- Shared styles and components, never page by page. New markup goes in `templates/components.html` and `app.css`; no inline `style=` or `on*=`.
- Toasts expire after 5 s and dismiss on click. No dismiss wording. Notifications never travel in URL parameters.
- Toasts are always in front of everything (dialogs, overlays, dropdowns, the sticky header). One container (`.toasts`), one token (`--z-toast`, the highest z-index in the stylesheet), rendered as a popover so it is in the top layer; while a modal dialog is open the container sits inside it (a modal makes the rest of the page inert, and a toast must stay clickable). No per-page z-index or toast placement. `TestToastsAreInFrontOfEverything` and the browser sweep enforce it.
- The top bar is sticky on every page and width (`position:sticky;top:0`, opaque, `--z-header`, below menus, dialogs and `--z-toast`). `--header-h` (kept by `app.js`) is the `scroll-padding-top`, so anchors and focus never land behind it. Below 720 px it is one row: brand, current page name, a 44 px Menu button; the links, version, user and logout fold into `.nav-panel`, which closes on Escape, a click outside and choosing a link. Wider windows keep the links in the bar. Standalone screens have no bar. The browser sweep checks sticky, opaque, not covered, anchors and the panel at all seven widths.
- Row actions: the one or two used most stay visible (Test, Copy URL); the rest go in the shared `menu_start`/`menu_end` More menu (button with `aria-haspopup`/`aria-expanded`, list fixed to the window, arrow keys, Escape, outside click, 44 px items on phones). No per-page dropdowns. The name opens the details dialog, and the dialog offers the same actions as the row.
- A dialog with several settings is split in `.dsec` sections (`section_head`), each with its own Save. A save posts by `fetch` (`form[data-save]`), updates the page in place and never reloads away another section's edits. Unsaved edits are tracked (dirty chip); Close, Escape and Back ask before discarding. Form modals do not close on a click outside.
- Short facts on a phone card (type, toggles) carry `class="inline"` so they share a line without labels. A card stays under about 270 px (its controls are 44 px).
- List tables are tables above 720 px and a stack of cards below it, from the same markup. Every `td` carries `data-label` (its column title); `primary` marks the name (the card title), `status` the pill beside it, `actions-cell` the button row. `TestTableCellsCarryTheirLabels` enforces it.
- Details dialogs and other detail screens list what the user can change first (forms, selects, buttons) and the read-only information last, using the same markup everywhere. `TestDetailsDialogsPutEditableFirstAndInfoLast` enforces it.
- No page is rendered by a POST. Screens are GET and can be refreshed; a change is a POST that answers 303 to a screen. What a change has to show once (a new key or client secret, an import's outcome) is kept per session for ten minutes and shown at `/admin/results/<token>`; never put the secret itself in the URL. Creating or deleting something never accepts GET.
- An upstream is addressed by its alias everywhere: `/admin/upstreams/<alias>/edit`, `/test`, `/logs`, `/logs/stream`, `/logs/download`, `/tools` (GET), `/save`, `/toggle`, `/redetect`, `/process`, `/delete`, `/tools/save`, `/tools/suggest` (POST). No `?alias=` and no hidden alias field. Only adding (`/admin/upstreams/save`) has no alias in the path.
- Keep list rows short: name, kind or status, the toggles, and actions. Everything else (facts, URLs, limits, dates) lives in a Details dialog opened with `data-dialog-open` from the shared `dialog_content` template, so nothing becomes unreachable. Details never repeat what the row already shows.
- Standalone screens (consent, signed out, sign-in error, OIDC not configured) set `Solo` on the page: no header, one `.login` card centered both ways (`main.solo`, `min-height:100dvh`), brandmark first, no inline styles. The CSP drops `form-action` only on the consent page, so its redirect to the client works.
- `TestNoHorizontalScrollInBrowser` (Chrome) checks 320, 360, 390, 768, 1024, 1280 and 1920 px: no horizontal scroll on any page or Details dialog, solo cards centered. `SKGATE_SHOTS=<dir>` saves screenshots.
- Tables: one row per upstream/key/client, no subtitles under values. Detail that does not fit a column goes in a hover `title` (the alias box lists type, target, source, revision, host override). Buttons in a row's action cell share one minimum width (`.table .actions .act`) so the columns line up whatever the label.
- Forms: wrap fields in `<form class="form">` (`form wide` for the import box). Controls get no widths of their own; they fill their container, so every input, select, textarea and pairs row ends at the same right edge at any nesting depth. `TestFormsShareOneColumn` enforces it; `TestFormRightEdgesInBrowser` measures it in Chrome when `SKGATE_CHROME` and `SKGATE_PUPPETEER` are set.
- Every content dialog is addressed by the URL fragment (its template id, such as `#provider-grok`, `#key-<id>`, `#client-<id>`, `#upstream-<alias>`, `#helper-model`), all in the shared modal code in app.js: opening sets it, closing by the button, Escape or the backdrop clears it, Back closes, a load or hashchange opens. New dialogs get this by using `data-dialog-open` and `dialog_content`; never set or clear the fragment anywhere else. `TestModalFragmentInJSDOM` and the sweep enforce it.
- Backdrop clicks: a dialog closes by a click outside it only when it is marked informational (`infodlg`, `data-informational`), meaning it holds no form, field or button. Every dialog with a form or a decision (and every confirmation) ignores the backdrop; Escape and its buttons close it. The rule lives once in the shared modal in app.js; `TestOnlyInformationalDialogsCloseByTheBackdrop` enforces it.
- `?key=` is a per-key flag (`vkeys.url_key`, Details dialog, off by default), never a global setting. Anything that reads a key from the URL must check `Key.URLKey`. A highlighted row/card for such keys must use the shared tint token, not a one-off color.
- Dates are typed as plain text, never a native date picker: use the shared `expiry_field` (parsed by `vkeys.ParseExpiry`, live preview from `/admin/keys/expiry`, quick buttons). Add new accepted forms to the parser and its test, not to the script.
- List tables head their button column with `th_actions` ("Actions") and put Status first.
- No JS `alert`, `confirm` or `prompt`. One shared modal (`data-modal` for confirmations, `dialog` for content).
- A `data-confirm` form may sit inside a content dialog (regenerate and revoke are in the key dialog): the content is hidden, not removed, while it asks, so the form can still submit; Cancel and Escape go back to the content. Never clear the body of a dialog whose form is pending.
- Values with details (usage counts, and the like) use the `tip` component: short visible text, the rest in the hover tooltip. Large counts go through `numfmt.Compact` (K, M, B, T), exact numbers through `numfmt.Exact`; never format counts in a template.
- Status pills carry their details in a hover tooltip. No subtitle or parenthesis beside a pill. Use the `pill` component.
- No icons anywhere in the UI: no icon fonts, no symbol or emoji glyphs (a "more" menu says More, a warning says Warning). A thing is shown to be tappable with text and styling: a button-like box, a hint line, hover, focus and pressed states.
- SI naming rule: write "SI" (SuperIntelligence) instead of "AI" only in text that is entirely ours: UI copy, tooltips, docs, descriptions and disclaimers. Never rename an identifier, file name, config key, JSON key, environment variable, database column, URL, API path or parameter, or anything external. "AI" stays in:
  - Company and product names: OpenAI, OpenAI-compatible, Google AI Studio, xAI, SpaceXAI (they are names, not our wording).
  - URLs and hostnames: `aistudio.google.com`, `glama.ai`, `claude.ai`, `x.ai`, `api.mistral.ai`, `openrouter.ai`, `auth.x.ai`, `api.x.ai` (they would break).
  - Go identifiers and the JSON field the page script reads (`AI`, `AIWhy`, `"ai"`, `"aiWhy"` in the OpenAPI screens): renaming gains nothing and could break a cached script.
  - External protocol, spec and schema identifiers, for example the Glama schema URL `https://glama.ai/mcp/schemas/server.json`, and quoted third-party text.
  The tests `TestDocsSaySINotAI` and `TestScreensSaySINotAI` check text only.
- A button that is off for a reason uses the `gated_act` component (`gate` builds its data): disabled, the reason as tooltip and as visible text beside it, because touch has no hover.
- Updates of what skgate stores from outside are manual: a click re-reads, a review screen shows what would change, and nothing is replaced until the person confirms (cancel changes nothing). OpenAPI descriptions follow this (`oaupdate.go`); never poll or refresh on a timer. When the update passes through the helper model, the screen says "SI layer" and names no provider or model.
- A value people copy (an address, a client ID or secret, a key, a sign-in code) goes in the `copybox` component (`copyurl`, `copytext` or `copycode` build its data): the whole box is the button, it wraps instead of scrolling, says "Tap to copy", and answers with a toast. Do not add a separate Copy button or a bare `<code>` for such a value.
- Tokens are shown masked: asterisks plus the last 4 characters; under 8 characters, asterisks only. Refresh tokens appear only in the provider dialog.
- Name/value data (env, headers) and lists (args) use the dynamic rows components.
- Manual input is never gated behind an account or a helper.
- The helper model picker lists Grok's models, every skgate-defined alias (never the aliases a provider reports itself) and one group per other ready provider; `Proxy.Post` and `Proxy.Stream` resolve skgate aliases like `/v1`, so the helper can be set to one.
- Providers: Grok (OAuth) is first and serves requests that name no other provider's model. The key-based providers are data in `internal/provider/keyed/presets.go` (id, name, base URL, key hint, docs link, adapter): a new OpenAI-compatible provider is one line there, nothing else. Their keys are stored with `SetSecret` (`provider.<id>.key`), never logged, never sent to a model, shown masked. Anthropic is the only adapter (`keyed/anthropic.go`, a `Transport`).
- The proxy holds a pool of providers (`Proxy.Add`). Routing is in `provider/route.go`: alias (provider + model), then the first ready provider whose cached model list has the model, then the first ready provider. Optional interfaces `Readier`, `Static` (no 401 refresh), `ListFilter` and `Transport` keep the core small. Alias names are unique across providers.
- The helper model setting stays under the first provider's id; its value is a model name routed like a request. Boot work (warming model lists) runs in the background and must never block or fail startup: Glama builds with `go build ./cmd/skgate` and runs it with placeholder env and no reachable OIDC issuer.
- The status pill of a provider is a plain status (`pill`): no hover swap, not clickable. Signing out is an explicit **Sign out** button (`act danger`, with its confirmation) beside **Sign in again**. **Sign in again** is secondary (`act`) while signed in and healthy and primary (`btn`) when signed out or the token is failing.
- Reasoning: the dropdown labelled "Reasoning" (auto = the model decides, low, medium, high; a stored "default" still reads as auto) sits directly under the model dropdown inside the shared `model_picker`, same form so one Save covers both, no one-off CSS (`.pick`). It belongs to the MCP helper model only and defaults to auto; a choice saved earlier keeps working. There is no reasoning setting for chat: proxied requests go through as the client wrote them. A provider that rejects `reasoning_effort` gets the Suggest request again without it.
- Helper timeout: a number field in the same picker (seconds without an answer, default 120, a whole number from 1 to 86400), with a note that more capable models take longer. For models that look like heavy reasoners (`provider.LooksFrontier`, by name) the picker suggests 600 s as text only; a suggestion is never applied for the user.
- A Suggest timeout (silence or overall cap) shows a distinct state with the stage and elapsed time, the toast reason, and a Lower reasoning button that opens the helper model dialog. The log line names the same stage and reasoning.
- The process output is one viewer (`.logbar`, `.logview`, `.logline`, `.logdivider` in `app.css`, `[data-log]` in `app.js`): the page renders the kept lines, then follows a server-sent stream that exists only while the page is open (resumed with `Last-Event-ID`). Scrolling up stops following and offers "jump to latest". stdout, stderr and skgate's own lines are tinted and also named. Filters (text or `/regex/`, level, stderr only), the clock/ago toggle, copy (the shown lines) and download live in the bar; clear logs stays a danger action. New output features extend this viewer, not a second one.
- OpenAPI upstreams (`internal/openapi`, `internal/mcp/openapi.go`, `internal/admin/openapi.go`): skgate answers MCP itself and makes the HTTP calls, so no process and no slim-image limit. The description is stored as normalized JSON in `upstream_openapi` with the chosen operations (keys are `METHOD /path`); the credential is on the `upstreams` row and is applied last. Read verbs start on, write verbs off, and the tool counter (`mcp.ToolsGood` 15, `mcp.ToolsWarn` 30; green, amber, red) must stay prominent on the tools page, the list and the Overview. **Never block on the count**: it only informs, and Save always works. The repair and naming prompts (`internal/openapi/assist.go`) are separate from the Suggest prompt, run on the helper model, return patches or names that the admin approves, never see a credential, and never touch servers or security. A problem in a description never refuses an import. Schemas are inlined under a node budget and depth limit (`internal/openapi/schema.go`), because a small description can fan out to billions of nodes; keep every new walk over a description bounded the same way.
- Icons live in `internal/admin/static/` (`favicon.svg`, `favicon.ico`, `apple-touch-icon.png`, PNG sizes). Swap the files, keep the names.

## Security

- Secrets are never logged, never put in URLs, never sent to a model.
- Process output keeps the current and the previous run only, at most `LOG_LINES` (1000) lines or 512 KB, with lines cut at about 4 KB. Values of environment variables not marked plain, and the git token, are masked before a line is stored in memory or in `<MANAGED_DIR>/.logs/`. No cross-upstream search, no long-term storage.
- Never run `uv` or `npm` as root in the container. Managed servers run as `nobody` (65534), which owns the cache directory.
- Never edit stack YAML in docs or automation. Suggest the change instead.

## Code

- Small, meaningful commits with a message body. One concern per commit.
- Tests are required. Run `go test -race ./...`; `tools/chk.sh` (build, vet, tests of the staged tree) must pass for every commit. CI runs the same checks with the browser tests on (the `browser-tools` action sets up Chrome, jsdom and puppeteer) and `govulncheck` as its own job.
- README is for user-visible things only (features, runtimes, env variables, setup). Logging, timeouts, streaming, retries, TZ plumbing and the effort selector stay out of the front page; details go in `docs/` or the code. README edits go in their own small commits.
- Update README and `.env.example` in the same change. A test keeps their variables in sync with the code.
- Admin routes (`/admin/...`) work with and without a trailing slash through one router-level rewrite (`trimAdminSlash`); never add slash variants per page. It touches nothing outside `/admin` (`/mcp`, OAuth, `/.well-known`, `/v1` keep their own handling).
- Database changes are additive and backward compatible. Existing upstreams keep working.

## Release

The default branch is `master`. Raw links, templates and docs point at `master`, never `main`.

1. Bump `Version` in `internal/config/config.go` (semver only, no suffixes) and the version and image tags in `server.json` (the MCP registry entry; `TestRegistryFilesAgreeWithTheVersion` checks they match).
2. Full test run, then tag `vX.Y.Z` on the final commit.
3. Push `master` and the tag to GitHub. The Release workflow builds the images (`vX.Y.Z`, `X.Y.Z`, `latest`, slim variants) for amd64 and arm64 and pushes them to `ghcr.io/helv-io/skgate`. Then its `mcp-registry` job lists `server.json` in the official MCP registry through GitHub OIDC (stable tags only). It fails if `server.json` does not match the tag and skips a version the registry already has. Watch it: a failure there does not undo the images. The pinned `mcp-publisher` version and sha256 are in `release.yml`.
4. Deploy: `docker compose pull skgate && docker compose up -d skgate`.
- `CHANGELOG.md` is written by `tools/changelog` (workflow `Changelog` on each stable tag; details in docs/development.md). Never reword a section by hand-running `-all`; edit the file in a normal commit. To steer a generated entry, put `Changelog: <Section>: <text>` or `Changelog: skip` in the commit message. The wording rule above applies.
- Structured text fields (OpenAPI description, MCP import JSON) use the one code box: `data-code="json|yaml|toml|auto"` on the textarea, which stays the field of record. Do not add an editor library or anything that needs `unsafe-inline`; the CSP stays `default-src 'self'; style-src 'self'; script-src 'self'`. Details in docs/development.md ("Code box"). Script that sets such a field's value sends an input event.
