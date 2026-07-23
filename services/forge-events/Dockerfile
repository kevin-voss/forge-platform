# syntax=docker/dockerfile:1
# Build context must be the repository root (see compose.yaml / Makefile).

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY services/forge-events/go.mod services/forge-events/go.sum ./
RUN go mod download

COPY services/forge-events/cmd ./cmd
COPY services/forge-events/internal ./internal
# Repo-relative contracts/ for unit/contract tests that walk up from package dirs.
COPY contracts ./contracts

ENV FORGE_EVENT_SCHEMA_DIR=/src/contracts/events

RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forge-events ./cmd/forge-events

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=build /out/forge-events /app/forge-events
COPY --from=build /src/contracts/events /contracts/events
RUN chown -R appuser:appgroup /app /contracts

USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-events \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_NATS_URL=nats://nats:4222 \
	FORGE_EVENTS_STREAMS=build,deployment,runtime,application,agent \
	FORGE_EVENT_SCHEMA_DIR=/contracts/events \
	FORGE_SCHEMA_VALIDATION=strict \
	FORGE_SHUTDOWN_GRACE_SECONDS=10

ENTRYPOINT ["/app/forge-events"]
