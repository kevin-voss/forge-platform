SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-gateway
IMAGE := forge/forge-gateway:local
HOST_PORT ?= 4000

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	PORT=$${PORT:-8080} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-gateway} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	go run .

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-gateway .

run: docker-build
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit:
	go test ./...

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@# Confirm structured JSON startup logs.
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-gateway"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-gateway"'
	@# Invalid PORT fails startup (config validation).
	@! PORT=not-a-port go run . >/tmp/forge-gateway-bad-port.out 2>&1
	@grep -q 'PORT' /tmp/forge-gateway-bad-port.out
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health + structured logs + invalid PORT + graceful SIGTERM)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-gateway-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	rm -rf bin
	@rm -f /tmp/forge-gateway-bad-port.out /tmp/forge-gateway-gofmt.out
