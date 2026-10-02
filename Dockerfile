# syntax=docker/dockerfile:1
#
# Targets:
#   slim  proxy only (static binary on distroless); managed upstreams are unavailable.
#   full  slim + managed runtimes: Node.js (npm, npx), Python 3 (pip, venv), uv/uvx, git, tini.
# The last stage is the default target, so a plain `docker build .` produces `full`.
#   docker build --target slim -t skgate:slim .
#   docker build -t skgate:latest .

ARG GO_VERSION=1.24
ARG NODE_VERSION=22
ARG UV_VERSION=0.12

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

FROM node:${NODE_VERSION}-bookworm-slim AS full
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git python3 python3-pip python3-venv tini \
 && rm -rf /var/lib/apt/lists/* /opt/yarn-* /usr/local/bin/yarn /usr/local/bin/yarnpkg
COPY --from=uv /uv /uvx /usr/local/bin/
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
    PIP_DISABLE_PIP_VERSION_CHECK=1
VOLUME /data
EXPOSE 8080
# Same root start and PUID/PGID drop as slim. tini is PID 1 and reaps orphaned grandchildren.
USER 0:0
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/skgate", "healthcheck"]
ENTRYPOINT ["/usr/bin/tini", "--", "/skgate"]
