# syntax=docker/dockerfile:1
#
# Targets:
#   slim  proxy only (static binary on distroless); managed upstreams are unavailable.
#   full  slim + managed runtimes: Node.js (npm, npx), Python 3 (pip, venv), uv/uvx, .NET 10 SDK, Go, git, tini.
# The last stage is the default target, so a plain `docker build .` produces `full`.
#   docker build --target slim -t skgate:slim .
#   docker build -t skgate:latest .

ARG GO_VERSION=1.26
ARG NODE_VERSION=22
ARG UV_VERSION=0.12
ARG DOTNET_VERSION=10.0
ARG GO_RUNTIME_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG PKG=github.com/helv-io/skgate/internal/config
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/skgate-full ./cmd/skgate \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X ${PKG}.Edition=slim" -o /out/skgate-slim ./cmd/skgate
# /data: default owner of a fresh named volume; on start skgate re-owns it to PUID:PGID anyway.
RUN mkdir -p /out/data /out/data/cache

FROM gcr.io/distroless/static:nonroot AS slim
# The official MCP registry checks this label against the name in server.json.
LABEL io.modelcontextprotocol.server.name="io.github.helv-io/skgate" \
      org.opencontainers.image.description="OAuth MCP gateway. SI proxy with model aliases. Grok subscription as OpenAI-compatible API, no key." \
      org.opencontainers.image.source="https://github.com/helv-io/skgate"
COPY --from=build /out/skgate-slim /skgate
COPY --from=build --chown=1000:1000 /out/data /data
ENV DB_PATH=/data/skgate.db LISTEN_ADDR=:8080
VOLUME /data
EXPOSE 8080
# Start as root on purpose: skgate chowns the data dir to PUID:PGID (default 1000:1000) and then
# drops privileges itself (setgroups, setgid, setuid, verified) before serving. It never serves as
# root. If the container is started as a non-root user (compose `user:`), it skips both steps.
USER 0:0
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/skgate", "healthcheck"]
ENTRYPOINT ["/skgate"]

FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv

FROM mcr.microsoft.com/dotnet/sdk:${DOTNET_VERSION} AS dotnet

# Go toolchain for managed Go servers, without the docs, tests and samples.
FROM golang:${GO_RUNTIME_VERSION}-bookworm AS gotool
RUN rm -rf /usr/local/go/doc /usr/local/go/test /usr/local/go/misc /usr/local/go/api

FROM node:${NODE_VERSION}-bookworm-slim AS full
# The official MCP registry checks this label against the name in server.json.
LABEL io.modelcontextprotocol.server.name="io.github.helv-io/skgate" \
      org.opencontainers.image.description="OAuth MCP gateway. SI proxy with model aliases. Grok subscription as OpenAI-compatible API, no key." \
      org.opencontainers.image.source="https://github.com/helv-io/skgate"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git libgcc-s1 libssl3 libstdc++6 python3 python3-pip python3-venv tini zlib1g \
 && rm -rf /var/lib/apt/lists/* /opt/yarn-* /usr/local/bin/yarn /usr/local/bin/yarnpkg
COPY --from=uv /uv /uvx /usr/local/bin/
COPY --from=dotnet /usr/share/dotnet /usr/share/dotnet
RUN ln -s /usr/share/dotnet/dotnet /usr/local/bin/dotnet
COPY --from=gotool /usr/local/go /usr/local/go
RUN ln -s /usr/local/go/bin/go /usr/local/bin/go
COPY --from=build /out/skgate-full /skgate
COPY --from=build --chown=1000:1000 /out/data /data
# Managed children inherit only an allow list of these (see README). Package caches live on the
# data volume so they persist and the rest of the filesystem can be read-only.
ENV DB_PATH=/data/skgate.db LISTEN_ADDR=:8080 \
    MANAGED_DIR=/data/managed \
    XDG_CACHE_HOME=/data/cache \
    NPM_CONFIG_CACHE=/data/cache/npm \
    UV_CACHE_DIR=/data/cache/uv \
    UV_LINK_MODE=copy \
    PIP_CACHE_DIR=/data/cache/pip \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    DOTNET_ROOT=/usr/share/dotnet \
    NUGET_PACKAGES=/data/cache/nuget \
    DOTNET_CLI_TELEMETRY_OPTOUT=1 \
    DOTNET_NOLOGO=1 \
    DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1 \
    DOTNET_GENERATE_ASPNET_CERTIFICATE=false \
    DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1 \
    GOROOT=/usr/local/go \
    GOCACHE=/data/cache/go-build \
    GOPATH=/data/cache/gopath \
    GOMODCACHE=/data/cache/gomod \
    CGO_ENABLED=0
VOLUME /data
EXPOSE 8080
# Same root start and PUID/PGID drop as slim. tini is PID 1 and reaps orphaned grandchildren.
USER 0:0
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/skgate", "healthcheck"]
ENTRYPOINT ["/usr/bin/tini", "--", "/skgate"]
