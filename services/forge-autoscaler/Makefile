SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-autoscaler
IMAGE := forge/forge-autoscaler:local
HOST_PORT ?= 4112

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	PORT=$${PORT:-4112} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-autoscaler} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_AUTOSCALER_DB_URL=$${FORGE_AUTOSCALER_DB_URL:-postgres://forge:forge@127.0.0.1:5001/forge_autoscaler?sslmode=disable} \
	FORGE_AUTOSCALER_EVAL_INTERVAL_MS=$${FORGE_AUTOSCALER_EVAL_INTERVAL_MS:-15000} \
	FORGE_AUTOSCALER_METRIC_SOURCE=$${FORGE_AUTOSCALER_METRIC_SOURCE:-auto} \
	FORGE_OBSERVE_URL=$${FORGE_OBSERVE_URL:-http://127.0.0.1:4106} \
	FORGE_GATEWAY_ADMIN_URL=$${FORGE_GATEWAY_ADMIN_URL:-http://127.0.0.1:4000} \
	FORGE_EVENTS_URL=$${FORGE_EVENTS_URL:-http://127.0.0.1:4105} \
	FORGE_RUNTIME_URL=$${FORGE_RUNTIME_URL:-http://127.0.0.1:4102} \
	go run .

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-autoscaler .

run: docker-build
	@$(COMPOSE) up -d --build postgres $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit

test-unit:
	go test ./...

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-autoscaler"'
	@echo "Integration checks passed (health + structured logs)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-autoscaler-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	rm -rf bin
	@rm -f /tmp/forge-autoscaler-gofmt.out
