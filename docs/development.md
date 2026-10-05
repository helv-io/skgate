# Development

Part of the [skgate README](../README.md).

```sh
go build ./... && go vet ./... && go test ./...
docker build -t skgate .                  # full (default target)
docker build --target slim -t skgate:slim .
SKGATE_E2E=1 go test ./internal/mcp -run E2E   # opt-in: real npx and uvx servers, needs network
```

`SKGATE_E2E_CACHE=<dir>` keeps the npm and uv caches of the E2E run.

- Icons: `internal/admin/static/` holds `favicon.svg`, `favicon.ico`, `apple-touch-icon.png` and the PNG sizes; they are embedded and served without login at `/favicon.svg`, `/favicon.ico` and `/apple-touch-icon.png`. Replace the files to change them.
- Admin UI: server-rendered templates and one shared stylesheet in `internal/admin`. Design changes go through the shared CSS and components; tests forbid inline styles, inline handlers and query-string notifications.
- The SQLite schema migrates idempotently on start.

## Changelog

`CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/): `Unreleased`, then versions newest first with dates, each with Added, Changed, Fixed, Security and Removed (only the ones that apply). Users read it, so entries are plain sentences, and the wording rule in AGENT.md applies (the naming test checks this file too).

One program writes it, `tools/changelog`, for the backfill and for new versions alike:

- `go run ./tools/changelog -tag vX.Y.Z` adds that version once, after `Unreleased`, from the commits since the previous release tag, and refreshes the compare links. Running it again changes nothing.
- `go run ./tools/changelog -all [-notes releases.json]` rebuilds the whole file from the tags. With `-notes` (a JSON list of `{tag, body}`), a release's hand-written notes replace its commit lines. This produced the first file.
- Commits are sorted by their first word (Add, Fix, Remove, Harden and so on). Version bumps, merges and commits that only touch tests, docs, `.github`, `tools` or example files are left out.
- To choose the wording or section yourself, add a line to the commit message: `Changelog: Fixed: Saving a key no longer fails after a restart` (sections: Added, Changed, Fixed, Security, Removed) or `Changelog: skip`.
- `tools/changelog/folded.txt` lists tags that were never released (v0.12.2). Their commits go into the next release.

The `Changelog` workflow runs the program on every stable `vX.Y.Z` tag and pushes one commit, "Changelog for vX.Y.Z", to `master` with the built-in `GITHUB_TOKEN` (`contents: write`). Why it is safe:

- A push made with `GITHUB_TOKEN` starts no other workflow, so it cannot loop, and it runs no CI. The job is also skipped when the actor is `github-actions[bot]`.
- It is separate from Release and carries no tag, so images and the registry entry are unaffected, and a failure there publishes nothing.
- It lands after the tag. The tagged source therefore does not list its own version, and the version bump is still the last commit a person makes for a release. Pull before the next change.
- Fix the wording of a generated section by editing `CHANGELOG.md` in an ordinary commit (CI ignores that file). The program never rewrites an existing section.
- A manual run (Actions, Changelog, Run workflow) takes a tag if an entry is missing.

## Underlines mean "opens"

A dotted underline (with a pointer) tells the reader a click shows more, so it is only drawn where that is true. A `<details>` block is used only when it has content beyond its summary.

A one-line description is plain text, and app.js gives it the underline (`.expands`, `role="button"`, keyboard Enter/Space) only while the line is cut off by the column and a click shows the whole text; widen the window and the underline goes.

The `tooldesc` component in `components.html` does this for the Test page's tool list; use it for any other list of tools. Names and descriptions sit in a `.table.split` table (about 30/70; the name wraps, phones get cards). `TestToolDescriptionsAreClickableOnlyWhenTheyOpen` (with `testdata/descline.js`) checks it in a browser at three widths.

## Code box

The OpenAPI paste box (JSON, YAML or TOML) and the MCP import box (JSON) are code editors: syntax colours, the matching bracket, an underline on the first syntax error with its line in the status line, and auto-indent on Enter.

They use **CodeMirror 6**, vendored as one committed file, `internal/admin/static/codemirror.js` (about 369 KB minified, `go:embed`ded with the rest of `static/`, loaded only by `upstream_form.html` and `upstream_import.html`). No CDN, no fetch at run time.

Use it by marking a textarea `data-code="json"`, `"yaml"`, `"toml"` or `"auto"` (guessed from the text) and, for a status line, adding `<p class="muted" data-code-status aria-live="polite"></p>` in the same `.field`. A field that has `data-json-check` keeps that check's own line and its block on invalid JSON.

The textarea stays the form field of record: it keeps its name and value (updated on every change, with an `input` event), it is what the form sends, and it is all the page shows without script or in a browser without constructed stylesheets. Script that sets its `.value` must send an `input` event afterwards (the editor then follows).

**What is in the bundle** (exact versions in `tools/codemirror/package.json` and `package-lock.json`, licences in `THIRD_PARTY_NOTICES.md`): `@codemirror/state`, `view`, `commands`, `language`, `lint`, `lang-json`, `lang-yaml`, `legacy-modes` (TOML highlighting), `@lezer/highlight` (all MIT), and `smol-toml` (BSD-3-Clause, the TOML error check). Our own part is `tools/codemirror/entry.js` (about 150 lines: the languages, the three error checks, the YAML Enter rule, the theme and the textarea sync). Errors: JSON through `JSON.parse`, TOML through `smol-toml`, YAML through the syntax tree's error nodes. skgate's own parser still decides when the form is sent, and the paste box never blocks the form.

**How it stays CSP-clean** (`style-src 'self'`): CodeMirror adds its rules as a `<style>` element when it lives in the page, which the CSP refuses. The editor therefore lives in a **shadow root**, where it uses constructed stylesheets (`adoptedStyleSheets`), which the CSP allows. The page's CSS variables reach into the shadow root, so the colours and sizes come from `app.css` (`--code-size`, 16 px on touch screens, `--acc`, `--ok` and so on). The error underline is drawn with CSS, not CodeMirror's data: image, which `default-src 'self'` blocks. If constructed stylesheets are missing (an old browser) the script leaves the textarea alone. `TestCodeBoxInBrowser` fails on any CSP violation or page error and checks that no `<style>` element exists.

**Update or rebuild** (needs Node 20+ and npm; the bundle is committed, so nothing else needs them):

```
cd tools/codemirror
npm ci                       # the exact versions of package-lock.json
npm run build                # writes internal/admin/static/codemirror.js and ../../THIRD_PARTY_NOTICES.md
```

To upgrade, run `npm install --save-exact <package>@<version>` for the packages you want, then `npm run build`, run `TestCodeBoxInBrowser` and the layout sweep, and commit `package.json`, `package-lock.json`, `codemirror.js` and `THIRD_PARTY_NOTICES.md` together. `node_modules` is not committed.

**Tests:** `internal/app/testdata/codebox.js` runs in a real browser (`TestCodeBoxInBrowser`: colours, underline, bracket match, auto-indent, the textarea kept as the field, 16 px on touch screens, no CSP violation, no `<style>` element); `TestCodeBoxIsWiredIntoBothFields` checks the markup, that the bundle is served from our own origin without fetch or eval, and that the CSP is unchanged; `importjson.js` checks that the JSON check still blocks Import; the layout sweep covers the import page.
