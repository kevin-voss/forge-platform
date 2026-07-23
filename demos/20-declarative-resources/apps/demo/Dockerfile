FROM python:3.12-alpine
WORKDIR /app
COPY server.py /app/server.py
ARG VERSION=v1
ARG READY_FAIL=false
ENV PORT=8080
ENV VERSION=${VERSION}
ENV READY_FAIL=${READY_FAIL}
EXPOSE 8080
CMD ["python", "/app/server.py"]
