# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
COPY main.go ./
COPY internal ./internal
COPY db ./db

RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forge-network .

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=build /out/forge-network /app/forge-network
RUN chown -R appuser:appgroup /app

USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-network \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	FORGE_DATABASE_SCHEMA=network \
	FORGE_DATABASE_MIGRATE_ON_START=true \
	FORGE_NETWORK_CLUSTER_CIDR=10.100.0.0/16 \
	FORGE_NETWORK_NODE_PREFIX_LEN=24 \
	FORGE_NETWORK_IPV6_ENABLED=false \
	FORGE_NETWORK_LEASE_RECLAIM_INTERVAL_S=60

ENTRYPOINT ["/app/forge-network"]
