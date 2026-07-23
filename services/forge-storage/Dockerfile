# syntax=docker/dockerfile:1

FROM rust:1-bookworm AS build
WORKDIR /src

COPY Cargo.toml Cargo.lock ./
COPY src ./src
COPY tests ./tests
COPY migrations ./migrations

RUN cargo test \
	&& cargo build --release \
	&& cp target/release/forge-storage /forge-storage

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser \
	&& mkdir -p /data/storage \
	&& chown -R appuser:appgroup /app /data

COPY --from=build /forge-storage /app/forge-storage
RUN chown -R appuser:appgroup /app

USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-storage \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	FORGE_STORAGE_ROOT=/data/storage \
	FORGE_STORAGE_ALLOWED_BASE=/data \
	FORGE_AUTH_MODE=dev

ENTRYPOINT ["/app/forge-storage"]
