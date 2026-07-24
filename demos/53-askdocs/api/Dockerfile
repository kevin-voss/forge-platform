# syntax=docker/dockerfile:1
# Build context: demos/53-askdocs (parent of api/ + migrations/).

FROM python:3.12-alpine
WORKDIR /app
RUN apk add --no-cache libpq
COPY api/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt
COPY api/store.py api/server.py api/test_store.py /app/
COPY migrations /migrations
ENV MIGRATIONS_DIR=/migrations \
	PORT=8080 \
	FORGE_SERVICE_NAME=askdocs-api \
	PYTHONUNBUFFERED=1
# Import check only — Postgres-backed unittest needs a live DB (run on host/CI).
RUN python -c "import store, server; print('imports ok')"
EXPOSE 8080
CMD ["python", "/app/server.py"]
