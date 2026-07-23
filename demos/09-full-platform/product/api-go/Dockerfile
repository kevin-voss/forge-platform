# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/incident-api .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/incident-api /incident-api
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080 \
	FORGE_SERVICE_NAME=incident-api \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_EVENTS_URL=http://host.docker.internal:4105 \
	FORGE_PRODUCT_AUTH=dev \
	FORGE_OTEL_ENABLED=true \
	FORGE_OTEL_EXPORTER_ENDPOINT=http://host.docker.internal:4317 \
	FORGE_STORAGE_URL=http://host.docker.internal:4107 \
	FORGE_STORAGE_BUCKET=artifacts
ENTRYPOINT ["/incident-api"]
