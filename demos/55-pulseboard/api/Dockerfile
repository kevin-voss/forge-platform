# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pulseboard-api .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/pulseboard-api /pulseboard-api
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080
# Demo default: Prometheus-compatible Observe metrics sidecar on the cluster net.
ENV FORGE_OBSERVE_URL=http://demo55-metrics:8080
ENV PULSEBOARD_APPLICATION=pulseboard-api
ENV FORGE_SERVICE_NAME=pulseboard-api
ENV FORGE_OTEL_ENABLED=false
ENTRYPOINT ["/pulseboard-api"]
