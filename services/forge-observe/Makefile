SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-observe
IMAGE := forge/forge-observe:local
HOST_PORT ?= 4106

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	PORT=$${PORT:-4106} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-observe} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_LOKI_URL=$${FORGE_LOKI_URL:-http://127.0.0.1:3003} \
	FORGE_TEMPO_URL=$${FORGE_TEMPO_URL:-http://127.0.0.1:3002} \
	FORGE_PROMETHEUS_URL=$${FORGE_PROMETHEUS_URL:-http://127.0.0.1:3001} \
	FORGE_BACKEND_TIMEOUT_MS=$${FORGE_BACKEND_TIMEOUT_MS:-2000} \
	FORGE_OBSERVE_READY_REQUIRE_BACKENDS=$${FORGE_OBSERVE_READY_REQUIRE_BACKENDS:-loki,tempo,prometheus} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	go run ./cmd/forge-observe

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-observe ./cmd/forge-observe

run: docker-build
	@$(COMPOSE) up -d loki tempo prometheus
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
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; b=json.load(sys.stdin); assert b["service"]=="forge-observe" and b["language"]=="go" and b["status"]=="running"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/health/backends" | python3 -c 'import sys,json; b=json.load(sys.stdin); assert b["loki"]=="ok" and b["tempo"]=="ok" and b["prometheus"]=="ok"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-observe"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-observe"'
	@test -f $(ROOT_DIR)/docs/contracts/observability-correlation.md && echo correlation-contract-present
	@# One required backend down → ready 503; backends endpoint shows failing one.
	@$(COMPOSE) stop loki >/dev/null
	@for i in $$(seq 1 30); do \
		code="$$(curl -s -o /tmp/forge-observe-ready-503.json -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/health/ready" || true)"; \
		if [[ "$$code" = "503" ]]; then break; fi; \
		sleep 1; \
	done
	@code="$$(curl -s -o /tmp/forge-observe-ready-503.json -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/health/ready" || true)"; \
		test "$$code" = "503"; \
		grep -q '"status":"not_ready"' /tmp/forge-observe-ready-503.json
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/health/backends" | python3 -c 'import sys,json; b=json.load(sys.stdin); assert b["loki"]=="down" and b["tempo"]=="ok" and b["prometheus"]=="ok"'
	@$(COMPOSE) start loki >/dev/null
	@for i in $$(seq 1 90); do \
		if curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'; then break; fi; \
		sleep 1; \
	done
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@# Restart Observe → reconnects to backends.
	@$(COMPOSE) restart $(SERVICE) >/dev/null
	@$(MAKE) wait
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/health/backends" | python3 -c 'import sys,json; b=json.load(sys.stdin); assert b["loki"]=="ok" and b["tempo"]=="ok" and b["prometheus"]=="ok"'
	@# Invalid PORT fails startup (config validation).
	@! PORT=not-a-port FORGE_LOKI_URL=http://127.0.0.1:3003 go run ./cmd/forge-observe >/tmp/forge-observe-bad-port.out 2>&1
	@grep -q 'PORT' /tmp/forge-observe-bad-port.out
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@grep -q '4106' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@echo "Integration checks passed (health + identity + backends ok/down + ready gating + restart + invalid PORT + graceful SIGTERM + correlation contract + port 4106)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-observe-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) -f Dockerfile $(ROOT_DIR)

docker-run: run

clean:
	rm -rf bin
	@rm -f /tmp/forge-observe-bad-port.out /tmp/forge-observe-gofmt.out /tmp/forge-observe-ready-503.json
