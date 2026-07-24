# syntax=docker/dockerfile:1
# Build context: demos/53-askdocs (demo root) so api/ modules can be copied.

FROM python:3.12-alpine
WORKDIR /app
RUN apk add --no-cache libpq
COPY worker/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt
COPY worker/worker.py worker/storage.py worker/events.py /app/
COPY api/store.py api/chunking.py api/embeddings.py api/models.py api/memory.py \
	api/ingest_embed.py /app/
ENV PORT=8080 \
	FORGE_SERVICE_NAME=askdocs-worker \
	PYTHONUNBUFFERED=1 \
	FORGE_STORAGE_URL=http://host.docker.internal:4107 \
	FORGE_STORAGE_BUCKET=askdocs-corpus \
	FORGE_STORAGE_PROJECT=askdocs \
	FORGE_EVENTS_URL=http://host.docker.internal:4105 \
	FORGE_EVENTS_CONSUMER=askdocs-documents \
	FORGE_EVENTS_SUBJECT=document.uploaded \
	FORGE_MODELS_URL=http://host.docker.internal:4300 \
	FORGE_MEMORY_URL=http://host.docker.internal:4303 \
	FORGE_MEMORY_PROJECT=askdocs \
	FORGE_MEMORY_COLLECTION=askdocs-chunks \
	FORGE_MODELS_EMBED_MODEL=local-embed-small \
	FORGE_MODELS_EMBED_DIM=384
RUN python -c "import worker, storage, events, store, chunking, embeddings, models, memory, ingest_embed; print('imports ok')"
EXPOSE 8080
CMD ["python", "/app/worker.py"]
