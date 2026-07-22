# syntax=docker/dockerfile:1

FROM rust:1-bookworm AS build
WORKDIR /src

COPY Cargo.toml Cargo.lock ./
COPY src ./src
COPY migrations ./migrations

RUN cargo test \
	&& cargo build --release \
	&& cp target/release/forge-secrets /forge-secrets

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=build /forge-secrets /app/forge-secrets
COPY --from=build /src/migrations /app/migrations
RUN chown -R appuser:appgroup /app

USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-secrets \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	FORGE_SECRETS_MASTER_KEY_ID=m1

ENTRYPOINT ["/app/forge-secrets"]
