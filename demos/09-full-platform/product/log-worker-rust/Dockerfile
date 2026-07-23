# syntax=docker/dockerfile:1

FROM rust:1-bookworm AS build
WORKDIR /src

COPY Cargo.toml ./
COPY src ./src
RUN cargo test \
	&& cargo build --release \
	&& cp target/release/incident-log-worker /incident-log-worker

FROM gcr.io/distroless/cc-debian12:nonroot
WORKDIR /
COPY --from=build /incident-log-worker /incident-log-worker
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080 \
	FORGE_SERVICE_NAME=incident-log-worker \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_EVENTS_URL=http://host.docker.internal:4105
ENTRYPOINT ["/incident-log-worker"]
