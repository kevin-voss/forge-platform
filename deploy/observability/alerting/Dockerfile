FROM python:3.12-alpine
WORKDIR /app
COPY webhook_sink.py /app/webhook_sink.py
EXPOSE 8080
CMD ["python", "/app/webhook_sink.py"]
