FROM python:3.12-alpine
WORKDIR /app
COPY server.py test_server.py /app/
RUN python -m unittest -v test_server.py
ENV PORT=8080 \
    FORGE_SERVICE_NAME=demo-secrets \
    FORGE_SERVICE_VERSION=0.1.0 \
    FORGE_LOG_LEVEL=info \
    FORGE_ENV=development
EXPOSE 8080
CMD ["python", "/app/server.py"]
