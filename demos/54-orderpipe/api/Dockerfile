# syntax=docker/dockerfile:1
# Build context: demos/54-orderpipe (parent of api/ + migrations/).

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY api/go.mod api/go.sum* ./
RUN go mod download 2>/dev/null || true

COPY api/*.go ./
COPY migrations /migrations
ENV MIGRATIONS_DIR=/migrations
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/orderpipe-api .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/orderpipe-api /orderpipe-api
COPY --from=build /migrations /migrations
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080
ENV MIGRATIONS_DIR=/migrations
# Peer addressing via Forge Discovery (54.02) — never compose DNS.
ENV FULFILLMENT_URL=http://fulfillment.svc.forge:8080
ENV NOTIFY_URL=http://notify.svc.forge:8080
ENV FORGE_DISCOVERY_URL=http://host.docker.internal:4109
ENV FORGE_DISCOVERY_PROJECT=orderpipe
ENV FORGE_DISCOVERY_ENVIRONMENT=local
ENTRYPOINT ["/orderpipe-api"]
