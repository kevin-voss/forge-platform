# syntax=docker/dockerfile:1
# Build context: demos/54-orderpipe (parent of fulfillment/).

FROM python:3.12-alpine
WORKDIR /app
COPY fulfillment/server.py fulfillment/test_health.py /app/
ENV PORT=8080 \
	FORGE_SERVICE_NAME=orderpipe-fulfillment \
	PYTHONUNBUFFERED=1
RUN python -c "import server; print('imports ok')" \
	&& python -m unittest -v test_health.py
EXPOSE 8080
CMD ["python", "/app/server.py"]
