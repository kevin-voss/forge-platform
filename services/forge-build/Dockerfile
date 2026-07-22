# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

RUN apt-get update \
	&& apt-get install -y --no-install-recommends git \
	&& rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
COPY main.go ./
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forge-build .

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl git \
	&& rm -rf /var/lib/apt/lists/* \
	&& mkdir -p /workspace /var/lib/forge-build \
	&& chmod 0700 /workspace /var/lib/forge-build

COPY --from=build /out/forge-build /app/forge-build

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-build \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_AUTH_MODE=dev \
	DOCKER_HOST=unix:///var/run/docker.sock \
	FORGE_BUILD_WORKSPACE_DIR=/workspace \
	FORGE_BUILD_STORE_DIR=/var/lib/forge-build \
	FORGE_BUILD_RETENTION_HOURS=72 \
	FORGE_BUILD_CLEANUP_ON_START=true \
	FORGE_DEFAULT_FORGE_YAML=forge.yaml \
	FORGE_BUILD_TIMEOUT_SECONDS=600 \
	FORGE_BUILD_MAX_CONCURRENCY=2 \
	FORGE_BUILD_LOG_BUFFER_LINES=5000 \
	FORGE_SHUTDOWN_GRACE_SECONDS=10

# Privileged local-dev: Docker socket is typically root-owned mode 660.
# Run as root for the socket mount (documented; harden later).
USER root:root

ENTRYPOINT ["/app/forge-build"]
