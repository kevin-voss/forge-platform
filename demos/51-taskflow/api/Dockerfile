# syntax=docker/dockerfile:1
# Build context: demos/51-taskflow (parent of api/ + migrations/).

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY api/go.mod api/go.sum* ./
RUN go mod download 2>/dev/null || true

COPY api/*.go ./
COPY migrations /migrations
ENV MIGRATIONS_DIR=/migrations
RUN CGO_ENABLED=0 GOOS=linux go test ./... \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/taskflow-api .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/taskflow-api /taskflow-api
COPY --from=build /migrations /migrations
USER nonroot:nonroot
EXPOSE 8080
ENV PORT=8080
ENV MIGRATIONS_DIR=/migrations
# Identity reachable via host-published port from workload bridge network (51.03).
# JWT_SIGNING_KEY and DATABASE_URL are injected at deploy via Forge Secrets (51.04).
ENV FORGE_IDENTITY_URL=http://host.docker.internal:4002
ENV FORGE_PRODUCT_AUTH=enforce
ENTRYPOINT ["/taskflow-api"]
