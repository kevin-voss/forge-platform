# syntax=docker/dockerfile:1
# Build context: demos/54-orderpipe (parent of fulfillment/).

FROM python:3.12-alpine
WORKDIR /app
COPY fulfillment/server.py fulfillment/events.py fulfillment/test_health.py fulfillment/test_events.py /app/
ENV PORT=8080 \
	FORGE_SERVICE_NAME=orderpipe-fulfillment \
	FORGE_EVENTS_URL=http://host.docker.internal:4105 \
	FORGE_EVENTS_CONSUMER=orderpipe-fulfill \
	FORGE_EVENTS_SUBJECT=order.charged \
	FORGE_EVENTS_PUBLISH_SUBJECT=order.fulfilled \
	PYTHONUNBUFFERED=1
RUN python -c "import server, events; print('imports ok')" \
	&& python -m unittest -v test_health.py test_events.py
EXPOSE 8080
CMD ["python", "/app/server.py"]
