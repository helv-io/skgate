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
