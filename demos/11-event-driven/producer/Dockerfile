# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/demo-events-producer .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/demo-events-producer /demo-events-producer
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080 \
	FORGE_SERVICE_NAME=demo-events-producer \
	FORGE_SERVICE_VERSION=0.1.0 \
	FORGE_LOG_LEVEL=info \
	FORGE_ENV=development \
	FORGE_EVENTS_URL=http://forge-events:8080
ENTRYPOINT ["/demo-events-producer"]
