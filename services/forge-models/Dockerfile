# syntax=docker/dockerfile:1

FROM python:3.13-slim-bookworm AS build
WORKDIR /src

ENV UV_LINK_MODE=copy \
	UV_COMPILE_BYTECODE=1 \
	PIP_DISABLE_PIP_VERSION_CHECK=1 \
	PYTHONDONTWRITEBYTECODE=1

COPY --from=ghcr.io/astral-sh/uv:0.6.14 /uv /usr/local/bin/uv

COPY pyproject.toml uv.lock README.md ./
COPY app ./app
COPY tests ./tests

# Fail the image build if unit/contract tests fail.
RUN uv sync --frozen --extra dev \
	&& . .venv/bin/activate \
	&& pytest -q

FROM python:3.13-slim-bookworm AS runtime
WORKDIR /app

ENV UV_LINK_MODE=copy \
	UV_COMPILE_BYTECODE=1 \
	PIP_DISABLE_PIP_VERSION_CHECK=1 \
	PYTHONDONTWRITEBYTECODE=1

RUN apt-get update \
	&& apt-get install -y --no-install-recommends curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 10001 appgroup \
	&& useradd --uid 10001 --gid appgroup --create-home --shell /usr/sbin/nologin appuser

COPY --from=ghcr.io/astral-sh/uv:0.6.14 /uv /usr/local/bin/uv
# Depend on the build stage so tests always run before shipping the image.
COPY --from=build /src/pyproject.toml /src/uv.lock /src/README.md ./
COPY --from=build /src/app ./app

RUN uv sync --frozen --no-dev \
	&& chown -R appuser:appgroup /app

USER appuser:appgroup

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=forge-models \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_MODELS_BACKEND=fake \
	FORGE_SHUTDOWN_GRACE_SECONDS=10 \
	PYTHONUNBUFFERED=1 \
	PATH="/app/.venv/bin:$PATH"

ENTRYPOINT ["sh", "-c", "exec uvicorn app.main:create_app --factory --host 0.0.0.0 --port \"${PORT}\" --no-access-log --timeout-graceful-shutdown \"${FORGE_SHUTDOWN_GRACE_SECONDS:-10}\""]
