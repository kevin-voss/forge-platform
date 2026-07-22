# syntax=docker/dockerfile:1

FROM rust:1-bookworm AS build
WORKDIR /src

COPY Cargo.toml Cargo.lock ./
COPY src ./src

RUN cargo test \
	&& cargo build --release \
	&& cp target/release/forge-runtime /forge-runtime

FROM debian:bookworm-slim AS runtime
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=build /forge-runtime /app/forge-runtime
RUN chown -R appuser:appgroup /app

# Docker socket mount is a privileged local-dev convenience. The process runs as
# appuser; Compose adds the host docker GID via group_add when needed.
USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-runtime \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_AUTH_MODE=dev \
	DOCKER_HOST=unix:///var/run/docker.sock \
	FORGE_SHUTDOWN_GRACE_SECONDS=10

ENTRYPOINT ["/app/forge-runtime"]
