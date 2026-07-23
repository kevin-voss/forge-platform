# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod ./
COPY *.go ./
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
	FORGE_ENV=development
ENTRYPOINT ["/incident-api"]
