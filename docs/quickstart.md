# Quickstart: try skgate without an identity provider

Part of the [skgate README](../README.md#quick-start).

[`docker-compose.quickstart.yml`](../docker-compose.quickstart.yml) runs skgate and [Dex](https://dexidp.io), a small OIDC provider with one fixed login. It needs Docker Compose 2.23.1 or newer and no accounts. It is for trying skgate on one machine, not for running it.

```sh
curl -O https://raw.githubusercontent.com/helv-io/skgate/main/docker-compose.quickstart.yml
docker compose -f docker-compose.quickstart.yml up -d
```

1. Open `http://localhost:8080/admin`.
2. Sign in as `admin@example.com` with the password `password`.
3. **mcp upstreams** > **Add upstream**, or **status** > Grok > **Sign in**. The [README examples](../README.md#examples) continue from here.

Stop it with `docker compose -f docker-compose.quickstart.yml down`; add `-v` to delete the data too.

## What it sets up

| Piece | Value |
| --- | --- |
| skgate | `http://localhost:8080`, data in the `skgate-data` volume |
| Dex | `http://dex.localhost:5556/dex`, in-memory storage, restarts forget sessions |
| Login | `admin@example.com` / `password` |
| OIDC client | `skgate` with a fixed secret, redirect `http://localhost:8080/admin/oidc/callback` |

Both ports are published on `127.0.0.1` only. Change the ports or the image tag (`latest`, `slim`) in a `.env` file next to the compose file; [`quickstart.env.example`](../quickstart.env.example) lists them.

## Notes

- The URLs are plain http on `localhost`. MCP clients on other machines cannot reach it.
- `dex.localhost` is loopback in current browsers. The compose file also gives it to skgate as a network alias, so both reach Dex under one name, which OIDC requires (the issuer must match exactly). If your browser does not resolve `*.localhost`, add `127.0.0.1 dex.localhost` to your hosts file.
- The login, the client secret and the password hash are public. Do not publish these ports or reuse the file as is.

## Going on

For a real setup use your own provider, an https `PUBLIC_URL` and the README [Quick start](../README.md#quick-start). Provider hints: [oidc.md](oidc.md).
