# syntax=docker/dockerfile:1
# Build context: demos/53-askdocs (demo root) so api/store.py + api/chunking.py can be copied.

FROM python:3.12-alpine
WORKDIR /app
RUN apk add --no-cache libpq
COPY worker/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt
COPY worker/worker.py worker/storage.py worker/events.py /app/
COPY api/store.py api/chunking.py /app/
ENV PORT=8080 \
	FORGE_SERVICE_NAME=askdocs-worker \
	PYTHONUNBUFFERED=1 \
	FORGE_STORAGE_URL=http://host.docker.internal:4107 \
	FORGE_STORAGE_BUCKET=askdocs-corpus \
	FORGE_STORAGE_PROJECT=askdocs \
	FORGE_EVENTS_URL=http://host.docker.internal:4105 \
	FORGE_EVENTS_CONSUMER=askdocs-documents \
	FORGE_EVENTS_SUBJECT=document.uploaded
RUN python -c "import worker, storage, events, store, chunking; print('imports ok')"
EXPOSE 8080
CMD ["python", "/app/worker.py"]
