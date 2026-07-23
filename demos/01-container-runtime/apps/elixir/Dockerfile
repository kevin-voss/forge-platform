# syntax=docker/dockerfile:1

ARG ELIXIR_IMAGE=hexpm/elixir:1.18.4-erlang-27.3.4.2-debian-bookworm-20250811-slim
ARG RUNTIME_IMAGE=debian:bookworm-20250811-slim

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
COPY test test
# Unit tests need test deps (ExUnit is stdlib); compile app and run tests before release.
RUN MIX_ENV=test mix deps.get \
	&& MIX_ENV=test mix test \
	&& MIX_ENV=prod mix compile \
	&& MIX_ENV=prod mix release

FROM ${RUNTIME_IMAGE} AS runtime
ENV LANG=C.UTF-8 \
	MIX_ENV=prod \
	HOME=/app \
	RELEASE_DISTRIBUTION=none \
	PORT=8080 \
	FORGE_SERVICE_NAME=demo-elixir-api \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development

RUN apt-get update \
	&& apt-get install -y --no-install-recommends openssl ca-certificates libncurses6 locales \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser

WORKDIR /app
COPY --from=build --chown=appuser:appuser /app/_build/prod/rel/demo_elixir_api ./

USER appuser:appuser
EXPOSE 8080

# No distributed Erlang / epmd cookie required for this demo.
# `start` runs the release in the foreground (Elixir 1.18+; no separate `foreground` command).
ENTRYPOINT ["/app/bin/demo_elixir_api"]
CMD ["start"]
