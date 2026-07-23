# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forge-infrastructure ./cmd/forge-infrastructure

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=build /out/forge-infrastructure /app/forge-infrastructure
RUN chown -R appuser:appgroup /app

USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-infrastructure \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_AUTH_MODE=dev \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	FORGE_DATABASE_SCHEMA=infrastructure \
	FORGE_DATABASE_MIGRATE_ON_START=true \
	FORGE_INFRA_RECONCILE_INTERVAL_MS=2000

ENTRYPOINT ["/app/forge-infrastructure"]
