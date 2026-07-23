# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
COPY main.go ./
COPY internal ./internal
COPY pkg ./pkg

RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forge-discovery .

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=build /out/forge-discovery /app/forge-discovery
RUN chown -R appuser:appgroup /app

USER appuser:appgroup

EXPOSE 8080 5053/udp

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-discovery \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_AUTH_MODE=dev \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	FORGE_DATABASE_SCHEMA=discovery \
	FORGE_DATABASE_MIGRATE_ON_START=true \
	FORGE_DISCOVERY_DNS_ENABLED=true \
	FORGE_DISCOVERY_DNS_PORT=5053 \
	FORGE_DISCOVERY_DNS_ZONE=svc.forge

ENTRYPOINT ["/app/forge-discovery"]
