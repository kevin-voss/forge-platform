# syntax=docker/dockerfile:1
# Build context: demos/52-snapnote/worker

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/snapnote-worker .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/snapnote-worker /snapnote-worker
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080
ENV FORGE_SERVICE_NAME=snapnote-worker
ENV FORGE_EVENTS_URL=http://host.docker.internal:4105
ENV FORGE_EVENTS_CONSUMER=snapnote-attachments
ENV FORGE_EVENTS_SUBJECT=attachment.uploaded
ENV FORGE_STORAGE_URL=http://host.docker.internal:4107
ENV FORGE_STORAGE_BUCKET=snapnote-attachments
ENV FORGE_STORAGE_PROJECT=snapnote
ENTRYPOINT ["/snapnote-worker"]
