# syntax=docker/dockerfile:1

FROM rust:1-bookworm AS build
WORKDIR /src

COPY Cargo.toml ./
COPY src ./src
RUN cargo test \
	&& cargo build --release \
	&& cp target/release/demo-rust-api /demo-rust-api

FROM gcr.io/distroless/cc-debian12:nonroot
WORKDIR /
COPY --from=build /demo-rust-api /demo-rust-api
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080 \
	FORGE_SERVICE_NAME=demo-rust-api \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development
ENTRYPOINT ["/demo-rust-api"]
