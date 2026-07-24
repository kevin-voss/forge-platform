# syntax=docker/dockerfile:1
# Build context: demos/53-askdocs (parent of api/ + migrations/).

FROM python:3.12-alpine
WORKDIR /app
RUN apk add --no-cache libpq
COPY api/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt
COPY api/store.py api/server.py api/chunking.py api/storage.py api/events.py \
	api/embeddings.py api/models.py api/memory.py api/retrieve.py api/ingest_embed.py \
	api/agents.py api/answer.py \
	api/test_store.py api/test_chunking.py api/test_embeddings.py api/test_answer.py /app/
COPY migrations /migrations
ENV MIGRATIONS_DIR=/migrations \
	PORT=8080 \
	FORGE_SERVICE_NAME=askdocs-api \
	PYTHONUNBUFFERED=1 \
	FORGE_STORAGE_URL=http://host.docker.internal:4107 \
	FORGE_STORAGE_BUCKET=askdocs-corpus \
	FORGE_STORAGE_PROJECT=askdocs \
	FORGE_EVENTS_URL=http://host.docker.internal:4105 \
	FORGE_EVENTS_SUBJECT=document.uploaded \
	FORGE_MODELS_URL=http://host.docker.internal:4300 \
	FORGE_MEMORY_URL=http://host.docker.internal:4303 \
	FORGE_MEMORY_PROJECT=askdocs \
	FORGE_MEMORY_COLLECTION=askdocs-chunks \
	FORGE_MODELS_EMBED_MODEL=local-embed-small \
	FORGE_MODELS_EMBED_DIM=384 \
	FORGE_AGENTS_URL=http://host.docker.internal:4301 \
	FORGE_AGENTS_PROJECT=askdocs \
	ASKDOCS_AGENT_NAME=askdocs-answerer
# Import check only — Postgres-backed unittest needs a live DB (run on host/CI).
RUN python -c "import store, server, chunking, storage, events, embeddings, models, memory, retrieve, ingest_embed, agents, answer; print('imports ok')"
EXPOSE 8080
CMD ["python", "/app/server.py"]
