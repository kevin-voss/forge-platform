# syntax=docker/dockerfile:1

FROM python:3.13.5-slim-bookworm

WORKDIR /app

COPY config.py jsonlog.py classify.py server.py main.py test_server.py otel_export.py ./

ENV FORGE_OTEL_ENABLED=true \
	FORGE_OTEL_EXPORTER_ENDPOINT=http://host.docker.internal:4318

RUN python -m unittest -v test_server.py \
	&& useradd --create-home --uid 10001 --shell /usr/sbin/nologin appuser \
	&& chown -R appuser:appuser /app

USER appuser:appuser

EXPOSE 8080

ENV PORT=8080 \
	FORGE_SERVICE_NAME=incident-classify \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	PYTHONUNBUFFERED=1

ENTRYPOINT ["python", "-u", "main.py"]
