# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/demo-app .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/demo-app /demo-app
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080 \
	FORGE_SERVICE_NAME=demo-app \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_OTEL_ENABLED=true \
	FORGE_OTEL_EXPORTER_ENDPOINT=http://host.docker.internal:4317
ENTRYPOINT ["/demo-app"]
