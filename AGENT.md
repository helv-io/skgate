# AGENT.md

Guidelines for changes to skgate. Applies to code, UI, docs and releases.

## Product principle

skgate must work out of the box. Users are not security experts.

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
- Row actions: the one or two used most stay visible (Test, Copy URL); the rest go in the shared `menu_start`/`menu_end` ⋯ menu (button with `aria-haspopup`/`aria-expanded`, list fixed to the window, arrow keys, Escape, outside click, 44 px items on phones). No per-page dropdowns. The name opens the details dialog, and the dialog offers the same actions as the row.
- A dialog with several settings is split in `.dsec` sections (`section_head`), each with its own Save. A save posts by `fetch` (`form[data-save]`), updates the page in place and never reloads away another section's edits. Unsaved edits are tracked (dirty chip); Close, Escape and Back ask before discarding. Form modals do not close on a click outside.
- Short facts on a phone card (type, toggles) carry `class="inline"` so they share a line without labels. A card stays under about 230 px.
- List tables are tables above 720 px and a stack of cards below it, from the same markup. Every `td` carries `data-label` (its column title); `primary` marks the name (the card title), `status` the pill beside it, `actions-cell` the button row; log lines use `compact`. `TestTableCellsCarryTheirLabels` enforces it.
- Details dialogs and other detail screens list what the user can change first (forms, selects, buttons) and the read-only information last, using the same markup everywhere. `TestDetailsDialogsPutEditableFirstAndInfoLast` enforces it.
- No page is rendered by a POST. Screens are GET and can be refreshed; a change is a POST that answers 303 to a screen. What a change has to show once (a new key or client secret, an import's outcome) is kept per session for ten minutes and shown at `/admin/results/<token>`; never put the secret itself in the URL. Creating or deleting something never accepts GET.
- An upstream is addressed by its alias everywhere: `/admin/upstreams/<alias>/edit`, `/test`, `/logs` (GET), `/save`, `/toggle`, `/redetect`, `/process`, `/delete` (POST). No `?alias=` and no hidden alias field. Only adding (`/admin/upstreams/save`) has no alias in the path.
- Keep list rows short: name, kind or status, the toggles, and actions. Everything else (facts, URLs, limits, dates) lives in a Details dialog opened with `data-dialog-open` from the shared `dialog_content` template, so nothing becomes unreachable. Details never repeat what the row already shows.
- Standalone screens (consent, signed out, sign-in error, OIDC not configured) set `Solo` on the page: no header, one `.login` card centered both ways (`main.solo`, `min-height:100dvh`), brandmark first, no inline styles. The CSP drops `form-action` only on the consent page, so its redirect to the client works.
- `TestNoHorizontalScrollInBrowser` (Chrome) checks 320, 360, 390, 768, 1024, 1280 and 1920 px: no horizontal scroll on any page or Details dialog, solo cards centered. `SKGATE_SHOTS=<dir>` saves screenshots.
- Tables: one row per upstream/key/client, no subtitles under values. Detail that does not fit a column goes in a hover `title` (the alias box lists type, target, source, revision, host override). Buttons in a row's action cell share one minimum width (`.table .actions .act`) so the columns line up whatever the label.
- Forms: wrap fields in `<form class="form">` (`form wide` for the import box). Controls get no widths of their own; they fill their container, so every input, select, textarea and pairs row ends at the same right edge at any nesting depth. `TestFormsShareOneColumn` enforces it; `TestFormRightEdgesInBrowser` measures it in Chrome when `SKGATE_CHROME` and `SKGATE_PUPPETEER` are set.
- Every content dialog is addressed by the URL fragment (its template id, e.g. `#provider-grok`, `#key-<id>`, `#client-<id>`, `#upstream-<alias>`, `#helper-model`), all in the shared modal code in app.js: opening sets it, closing by the button, Escape or the backdrop clears it, Back closes, a load or hashchange opens. New dialogs get this by using `data-dialog-open` and `dialog_content`; never set or clear the fragment anywhere else. `TestModalFragmentInJSDOM` and the sweep enforce it.
- Backdrop clicks: a dialog closes by a click outside it only when it is marked informational (`infodlg`, `data-informational`), meaning it holds no form, field or button. Every dialog with a form or a decision (and every confirmation) ignores the backdrop; Escape and its buttons close it. The rule lives once in the shared modal in app.js; `TestOnlyInformationalDialogsCloseByTheBackdrop` enforces it.
- `?key=` is a per-key flag (`vkeys.url_key`, Details dialog, off by default), never a global setting. Anything that reads a key from the URL must check `Key.URLKey`. A highlighted row/card for such keys must use the shared tint token, not a one-off color.
- Dates are typed as plain text, never a native date picker: use the shared `expiry_field` (parsed by `vkeys.ParseExpiry`, live preview from `/admin/keys/expiry`, quick buttons). Add new accepted forms to the parser and its test, not to the script.
- List tables head their button column with `th_actions` ("Actions") and put Status first.
- No JS `alert`, `confirm` or `prompt`. One shared modal (`data-modal` for confirmations, `dialog` for content).
- A `data-confirm` form may sit inside a content dialog (regenerate and revoke are in the key dialog): the content is hidden, not removed, while it asks, so the form can still submit; Cancel and Escape go back to the content. Never clear the body of a dialog whose form is pending.
- Values with details (usage counts, and the like) use the `tip` component: short visible text, the rest in the hover tooltip. Large counts go through `numfmt.Compact` (K, M, B, T), exact numbers through `numfmt.Exact`; never format counts in a template.
- Status pills carry their details in a hover tooltip. No subtitle or parenthesis beside a pill. Use the `pill` component.
- Tokens are shown masked: asterisks plus the last 4 characters; under 8 characters, asterisks only. Refresh tokens appear only in the provider dialog.
- Name/value data (env, headers) and lists (args) use the dynamic rows components.
- Manual input is never gated behind an account or a helper.
- The helper model picker lists the provider's models, then the skgate-defined aliases (never the aliases a provider reports itself); `Proxy.Post` and `Proxy.Stream` resolve skgate aliases like `/v1`, so the helper can be set to one.
- The status pill of a provider is a plain status (`pill`): no hover swap, not clickable. Signing out is an explicit **Sign out** button (`act danger`, with its confirmation) beside **Sign in again**. **Sign in again** is secondary (`act`) while signed in and healthy and primary (`btn`) when signed out or the token is failing.
- Reasoning: the dropdown labelled "Reasoning" (auto = the model decides, low, medium, high; a stored "default" still reads as auto) sits directly under the model dropdown inside the shared `model_picker`, same form so one Save covers both, no one-off CSS (`.pick`). It belongs to the MCP helper model only and defaults to auto; a choice saved earlier keeps working. There is no reasoning setting for chat: proxied requests go through as the client wrote them. A provider that rejects `reasoning_effort` gets the Suggest request again without it.
- Helper timeout: a number field in the same picker (seconds without an answer, default 120, any whole number), with a note that more capable models take longer. For models that look like heavy reasoners (`provider.LooksFrontier`, by name) the picker suggests 600 s as text only; a suggestion is never applied for the user.
- A Suggest timeout (silence or overall cap) shows a distinct state with the stage and elapsed time, the toast reason, and a Lower reasoning button that opens the helper model dialog. The log line names the same stage and reasoning.
- Icons live in `internal/admin/static/` (`favicon.svg`, `favicon.ico`, `apple-touch-icon.png`, PNG sizes). Swap the files, keep the names.

## Security

- Secrets are never logged, never put in URLs, never sent to a model.
- Never run `uv` or `npm` as root in the container. Managed servers run as `nobody` (65534), which owns the cache directory.
- Never edit stack YAML in docs or automation. Suggest the change instead.

## Code

- Small, meaningful commits with a message body. One concern per commit.
- Tests are required. Run `go test -race ./...`; `tools/chk.sh` (build, vet, tests of the staged tree) must pass for every commit.
- README is for user-visible things only (features, runtimes, env variables, setup). Logging, timeouts, streaming, retries, TZ plumbing and the effort selector stay out of the front page; details go in `docs/` or the code. README edits go in their own small commits.
- Update README and `.env.example` in the same change. A test keeps their variables in sync with the code.
- Admin routes (`/admin/...`) work with and without a trailing slash through one router-level rewrite (`trimAdminSlash`); never add slash variants per page. It touches nothing outside `/admin` (`/mcp`, OAuth, `/.well-known`, `/v1` keep their own handling).
- Database changes are additive and backward compatible. Existing upstreams keep working.

## Release

1. Bump `Version` in `internal/config/config.go` (semver only, no suffixes).
2. Full test run, then tag `vX.Y.Z` on the final commit.
3. Push `main` and the tag to GitHub. The Release workflow builds the images (`vX.Y.Z`, `X.Y.Z`, `latest`, slim variants) for amd64 and arm64 and pushes them to `ghcr.io/helv-io/skgate`.
4. Deploy: `docker compose pull skgate && docker compose up -d skgate`.
