# syntax=docker/dockerfile:1
# Build context: demos/54-orderpipe (demo root).

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

COPY notify/mix.exs notify/mix.lock ./
COPY notify/config config
RUN mix deps.get --only prod \
	&& mix deps.compile

COPY notify/lib lib
COPY notify/test test
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
	FORGE_SERVICE_NAME=orderpipe-notify \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_EVENTS_URL=http://host.docker.internal:4105 \
	FORGE_EVENTS_CONSUMER=orderpipe-notify \
	FORGE_EVENTS_SUBJECT=order.fulfilled \
	FORGE_EVENTS_PUBLISH_SUBJECT=order.notified

RUN apt-get update \
	&& apt-get install -y --no-install-recommends openssl ca-certificates libncurses6 locales \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser

WORKDIR /app
COPY --from=build --chown=appuser:appuser /app/_build/prod/rel/notify_elixir ./

USER appuser:appuser
EXPOSE 8080

ENTRYPOINT ["/app/bin/notify_elixir"]
CMD ["start"]
