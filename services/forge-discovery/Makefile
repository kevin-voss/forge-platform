SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-discovery
IMAGE := forge/forge-discovery:local
HOST_PORT ?= 4109

.PHONY: dev build run test test-unit test-integration lint format docker-build wait

dev:
	PORT=$${PORT:-8080} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-discovery} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_DATABASE_URL=$${FORGE_DATABASE_URL:-postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable} \
	FORGE_DATABASE_SCHEMA=$${FORGE_DATABASE_SCHEMA:-discovery} \
	FORGE_DATABASE_POOL_MAX=$${FORGE_DATABASE_POOL_MAX:-10} \
	FORGE_DATABASE_MIGRATE_ON_START=$${FORGE_DATABASE_MIGRATE_ON_START:-true} \
	FORGE_CONTROL_URL=$${FORGE_CONTROL_URL:-http://127.0.0.1:4001} \
	FORGE_OTEL_ENABLED=$${FORGE_OTEL_ENABLED:-false} \
	go run .

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-discovery .

run: docker-build
	@$(COMPOSE) up -d --build postgres forge-control $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 120
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 120

test: test-unit test-integration

test-unit:
	go test ./...

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"event":"discovery.started"'
	@curl -sf "http://127.0.0.1:4001/v1/kinds" | tee /tmp/forge-discovery-kinds.json | grep -q '"plural":"endpoints"'
	@grep -q '"plural":"services"' /tmp/forge-discovery-kinds.json
	@grep -q '"controller":"forge-discovery"' /tmp/forge-discovery-kinds.json
	@# Restart re-registers kinds idempotently.
	@$(COMPOSE) restart $(SERVICE) >/dev/null
	@$(MAKE) wait
	@curl -sf "http://127.0.0.1:4001/v1/kinds" | grep -q '"plural":"endpoints"'
	@# SIGTERM exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health + kinds + idempotent restart + graceful SIGTERM)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-discovery-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .
