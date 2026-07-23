# syntax=docker/dockerfile:1
# Build context must be the repository root (see compose.yaml / Makefile).

FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY services/forge-observe/go.mod services/forge-observe/go.sum ./
RUN go mod download

COPY services/forge-observe/cmd ./cmd
COPY services/forge-observe/internal ./internal
COPY docs/contracts/observability-correlation.md /contracts/observability-correlation.md
COPY contracts/openapi/forge-observe.openapi.yaml /contracts/openapi/forge-observe.openapi.yaml

# Correlation parity test walks up for docs/contracts; mirror layout for Docker tests.
RUN mkdir -p /src/docs/contracts /src/contracts/openapi \
	&& cp /contracts/observability-correlation.md /src/docs/contracts/observability-correlation.md \
	&& cp /contracts/openapi/forge-observe.openapi.yaml /src/contracts/openapi/forge-observe.openapi.yaml

RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forge-observe ./cmd/forge-observe

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=build /out/forge-observe /app/forge-observe
RUN chown -R appuser:appgroup /app

USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-observe \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_LOKI_URL=http://loki:3100 \
	FORGE_TEMPO_URL=http://tempo:3200 \
	FORGE_PROMETHEUS_URL=http://prometheus:9090 \
	FORGE_BACKEND_TIMEOUT_MS=2000 \
	FORGE_OBSERVE_READY_REQUIRE_BACKENDS=loki,tempo,prometheus \
	FORGE_SHUTDOWN_GRACE_SECONDS=10

ENTRYPOINT ["/app/forge-observe"]
