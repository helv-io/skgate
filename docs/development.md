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
