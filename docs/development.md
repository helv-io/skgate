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

## Code box

The OpenAPI paste box (JSON, YAML or TOML) and the MCP import box (JSON) are code boxes: syntax colours, the matching bracket beside the caret, a marker on the first syntax error with its line in the status line, and auto-indent on Enter. It is one shared component, "Code box" in `internal/admin/static/app.js` with `.codebox` and the `.t-*` colours in `app.css`. Use it by marking a textarea `data-code="json"`, `"yaml"`, `"toml"` or `"auto"` (guessed from the text) and, for a status line, adding `<p class="muted" data-code-status aria-live="polite"></p>` in the same `.field`. A field that has `data-json-check` keeps that check's own line.

- **No library is vendored.** CodeMirror 6 was considered and rejected: it needs an npm build to produce a bundle of several hundred KB, and it injects `<style>` tags for its theme, which the CSP (`style-src 'self'`) blocks; making it clean would mean a custom bundle with its own stylesheet and a build step in the repository. The component is about 300 lines of script instead (about 17 KB of script and 2 KB of CSS in the binary, no dependency, no licence notice needed). The CSP is unchanged and the browser test fails on any violation.
- **The textarea stays the form field.** The script puts a coloured copy of the text (`pre`, `aria-hidden`) behind it, with the same font, padding and border, and the textarea text is transparent over it. Name, value, validation, selection, the phone keyboard and the page without script are the browser's own. Script that sets `.value` must send an input event afterwards.
- **The readers colour and point at the likely mistake; they do not judge.** JSON is read fully. YAML and TOML are read line by line (tabs in indents, unclosed quotes and brackets, a second colon in plain text, a TOML line that is not `key = value`, a `[table]` or a comment). skgate's own parser decides when the form is sent, and the paste box never blocks the form.
- **Mobile:** 16 px text on touch screens (smaller text makes phones zoom in on focus), no Tab capture (Tab still moves focus), Enter is handled in `beforeinput` so phone keyboards that send no key event still indent.
- **Limits:** colours stop above 200 000 characters (the text stays readable); the text does not wrap (it scrolls inside the box).
- **Tests:** `internal/app/testdata/codebox.js` runs in a real browser (`TestCodeBoxInBrowser`; colours, error marker, bracket match, auto-indent, scroll in step, long text, no CSP violation); `TestCodeBoxIsWiredIntoBothFields` checks the markup and that the CSP is unchanged; the layout sweep covers the import page.
- **Changing it:** keep the copy and the textarea in the same font, line height, padding, border and tab size, or the letters drift apart. A new colour class needs a rule in `app.css`.
