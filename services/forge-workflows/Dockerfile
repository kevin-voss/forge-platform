# syntax=docker/dockerfile:1

ARG ELIXIR_IMAGE=hexpm/elixir:1.18.4-erlang-27.3.4-debian-bookworm-20250610-slim
ARG RUNTIME_IMAGE=debian:bookworm-20250610-slim

FROM ${ELIXIR_IMAGE} AS build
ENV MIX_ENV=prod \
	LANG=C.UTF-8
WORKDIR /app

RUN apt-get update \
	&& apt-get install -y --no-install-recommends build-essential git \
	&& rm -rf /var/lib/apt/lists/* \
	&& mix local.hex --force \
	&& mix local.rebar --force

COPY mix.exs mix.lock ./
COPY config config
RUN mix deps.get --only prod \
	&& mix deps.compile

COPY lib lib
COPY priv priv
COPY definitions definitions
COPY test test
# Unit tests need test deps (ExUnit is stdlib); DB/integration tests run via Makefile.
RUN MIX_ENV=test mix deps.get \
	&& MIX_ENV=test mix test --exclude integration --exclude db \
	&& MIX_ENV=prod mix compile \
	&& MIX_ENV=prod mix release

FROM ${RUNTIME_IMAGE} AS runtime
ENV LANG=C.UTF-8 \
	MIX_ENV=prod \
	HOME=/app \
	RELEASE_DISTRIBUTION=none \
	PORT=8080 \
	FORGE_SERVICE_NAME=forge-workflows \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	FORGE_WORKFLOWS_DEFS_DIR=/app/definitions

RUN apt-get update \
	&& apt-get install -y --no-install-recommends openssl ca-certificates libncurses6 locales curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser

WORKDIR /app
COPY --from=build --chown=appuser:appuser /app/_build/prod/rel/forge_workflows ./
COPY --from=build --chown=appuser:appuser /app/definitions /app/definitions

USER appuser:appuser
EXPOSE 8080

# `start` runs the release in the foreground (Elixir 1.18+).
ENTRYPOINT ["/app/bin/forge_workflows"]
CMD ["start"]
