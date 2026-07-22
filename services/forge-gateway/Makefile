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
	FORGE_CONTROL_URL=$${FORGE_CONTROL_URL:-http://127.0.0.1:4001} \
	FORGE_RUNTIME_URL=$${FORGE_RUNTIME_URL:-http://127.0.0.1:4102} \
	FORGE_ROUTE_SOURCE=$${FORGE_ROUTE_SOURCE:-control} \
	FORGE_ROUTE_SYNC_INTERVAL_SECONDS=$${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-10} \
	FORGE_HOST_PATTERN=$${FORGE_HOST_PATTERN:-'{service}.{project}.demo.localhost'} \
	FORGE_UPSTREAM_HOST=$${FORGE_UPSTREAM_HOST:-127.0.0.1} \
	FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS=$${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-5} \
	FORGE_UPSTREAM_PROBE_PATH=$${FORGE_UPSTREAM_PROBE_PATH:-/health/ready} \
	FORGE_UPSTREAM_FAILURE_THRESHOLD=$${FORGE_UPSTREAM_FAILURE_THRESHOLD:-3} \
	FORGE_UPSTREAM_SUCCESS_THRESHOLD=$${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-2} \
	FORGE_UPSTREAM_TRUST_RUNTIME_STATUS=$${FORGE_UPSTREAM_TRUST_RUNTIME_STATUS:-true} \
	FORGE_REQUEST_ID_HEADER=$${FORGE_REQUEST_ID_HEADER:-X-Request-Id} \
	FORGE_PROXY_CONNECT_TIMEOUT_SECONDS=$${FORGE_PROXY_CONNECT_TIMEOUT_SECONDS:-5} \
	FORGE_PROXY_RESPONSE_HEADER_TIMEOUT_SECONDS=$${FORGE_PROXY_RESPONSE_HEADER_TIMEOUT_SECONDS:-15} \
	FORGE_PROXY_OVERALL_TIMEOUT_SECONDS=$${FORGE_PROXY_OVERALL_TIMEOUT_SECONDS:-30} \
	FORGE_TRUST_INBOUND_XFF=$${FORGE_TRUST_INBOUND_XFF:-false} \
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
	@# Admin route replace + snapshot; no-match returns 404 envelope (proxy body covered by go test).
	@curl -sf -X PUT "http://127.0.0.1:$(HOST_PORT)/admin/routes" -H 'content-type: application/json' \
		-d '[{"host":"go.demo.localhost","upstreams":[{"url":"http://127.0.0.1:9"}],"strategy":"round_robin"}]' \
		| grep -q 'go.demo.localhost'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/admin/routes" | grep -q 'go.demo.localhost'
	@code="$$(curl -s -o /tmp/forge-gateway-nomatch.json -w '%{http_code}' -H 'Host: nope.localhost' "http://127.0.0.1:$(HOST_PORT)/")"; \
		test "$$code" = "404"; \
		grep -q '"code":"no_route"' /tmp/forge-gateway-nomatch.json
	@# Matched host with closed upstream → 502 (proves proxy dispatch, not open-proxy).
	@code="$$(curl -s -o /tmp/forge-gateway-badup.json -w '%{http_code}' -H 'Host: go.demo.localhost' "http://127.0.0.1:$(HOST_PORT)/")"; \
		test "$$code" = "502"; \
		grep -q '"code":"bad_gateway"' /tmp/forge-gateway-badup.json
	@# Passive failures trip threshold → 503 no_healthy_upstream (default failure threshold=3).
	@curl -s -o /dev/null -H 'Host: go.demo.localhost' "http://127.0.0.1:$(HOST_PORT)/" >/dev/null; \
		curl -s -o /dev/null -H 'Host: go.demo.localhost' "http://127.0.0.1:$(HOST_PORT)/" >/dev/null; \
		code="$$(curl -s -o /tmp/forge-gateway-nohealthy.json -w '%{http_code}' -H 'Host: go.demo.localhost' "http://127.0.0.1:$(HOST_PORT)/")"; \
		test "$$code" = "503"; \
		grep -q '"code":"no_healthy_upstream"' /tmp/forge-gateway-nohealthy.json
	@# Request id echoed on responses (generated when absent).
	@curl -si -H 'Host: nope.localhost' "http://127.0.0.1:$(HOST_PORT)/" \
		| tee /tmp/forge-gateway-reqid.headers | grep -qi '^x-request-id:'
	@# Client-provided request id preserved on 404 envelope path.
	@curl -si -H 'Host: nope.localhost' -H 'X-Request-Id: test-123' "http://127.0.0.1:$(HOST_PORT)/" \
		| tee /tmp/forge-gateway-reqid-reuse.headers | grep -qi '^x-request-id: test-123'
	@# Force sync refresh (Control /v1/endpoints missing → Runtime interim fallback).
	@curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/admin/routes/refresh" \
		| tee /tmp/forge-gateway-refresh.json | grep -q '"routesLoaded"'
	@python3 -c 'import json; d=json.load(open("/tmp/forge-gateway-refresh.json")); assert "routesLoaded" in d'
	@# Sync logs mention route sync (loop started or complete/failed).
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -E -q 'route sync'
	@# Upstream probe loop started.
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -E -q 'upstream probe loop started'
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health + admin routes + refresh sync + no_route/502/503 + request-id + probe loop + structured logs + invalid PORT + graceful SIGTERM)."

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
	@rm -f /tmp/forge-gateway-bad-port.out /tmp/forge-gateway-gofmt.out \
		/tmp/forge-gateway-nomatch.json /tmp/forge-gateway-badup.json \
		/tmp/forge-gateway-nohealthy.json /tmp/forge-gateway-refresh.json \
		/tmp/forge-gateway-reqid.headers /tmp/forge-gateway-reqid-reuse.headers
