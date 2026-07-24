# syntax=docker/dockerfile:1
# Build context: demos/52-snapnote (parent of api/ + migrations/).

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY api/go.mod api/go.sum* ./
RUN go mod download 2>/dev/null || true

COPY api/*.go ./
COPY migrations /migrations
ENV MIGRATIONS_DIR=/migrations
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/snapnote-api .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/snapnote-api /snapnote-api
COPY --from=build /migrations /migrations
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080
ENV MIGRATIONS_DIR=/migrations
# Storage reachable via host-published port from workload bridge network (52.02).
# Browser uploads use the SPA nginx /storage/ proxy (same-origin) against signed URLs.
ENV FORGE_STORAGE_URL=http://host.docker.internal:4107
ENV FORGE_STORAGE_PUBLIC_URL=http://app.snapnote.localhost:4000/storage
ENV FORGE_STORAGE_BUCKET=snapnote-attachments
ENV FORGE_STORAGE_PROJECT=snapnote
ENV FORGE_EVENTS_URL=http://host.docker.internal:4105
ENV FORGE_EVENTS_SUBJECT=attachment.uploaded
ENV FORGE_SERVICE_NAME=snapnote-api
ENTRYPOINT ["/snapnote-api"]
