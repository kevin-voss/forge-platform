FROM python:3.12-alpine
WORKDIR /app
RUN apk add --no-cache libpq \
	&& pip install --no-cache-dir "psycopg[binary]==3.2.9"
COPY server.py test_server.py /app/
RUN python -m unittest -v test_server.py
ENV PORT=8080 \
	FORGE_SERVICE_NAME=demo-managed-db \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_DEMO_FIXTURE_KEY=demo18-fixture \
	FORGE_DEMO_FIXTURE_VALUE=managed-db-ok
EXPOSE 8080
CMD ["python", "/app/server.py"]
