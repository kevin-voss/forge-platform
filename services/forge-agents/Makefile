SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-agents
IMAGE := forge/forge-agents:local
HOST_PORT ?= 4301

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait sync

sync:
	@command -v uv >/dev/null || (echo "uv is required (https://github.com/astral-sh/uv)" >&2; exit 1)
	uv sync --extra dev

dev: sync
	PORT=$${PORT:-4301} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-agents} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_MODELS_URL=$${FORGE_MODELS_URL:-http://127.0.0.1:4300} \
	FORGE_CONTROL_URL=$${FORGE_CONTROL_URL:-http://127.0.0.1:4001} \
	FORGE_RUNTIME_URL=$${FORGE_RUNTIME_URL:-http://127.0.0.1:4102} \
	FORGE_OBSERVE_URL=$${FORGE_OBSERVE_URL:-http://127.0.0.1:4106} \
	FORGE_STORAGE_URL=$${FORGE_STORAGE_URL:-http://127.0.0.1:4107} \
	FORGE_EVENTS_URL=$${FORGE_EVENTS_URL:-http://127.0.0.1:4105} \
	FORGE_AGENTS_DEFS_DIR=$${FORGE_AGENTS_DEFS_DIR:-} \
	FORGE_AGENTS_TOOLS_MODE=$${FORGE_AGENTS_TOOLS_MODE:-fake} \
	FORGE_AGENTS_TOOL_TIMEOUT_SECONDS=$${FORGE_AGENTS_TOOL_TIMEOUT_SECONDS:-15} \
	FORGE_AGENTS_DB_PATH=$${FORGE_AGENTS_DB_PATH:-/tmp/forge-agents-runs.db} \
	FORGE_AGENTS_MAX_CONCURRENT_RUNS=$${FORGE_AGENTS_MAX_CONCURRENT_RUNS:-4} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	uv run python -m app.main

build: sync
	uv sync --extra dev

run: docker-build
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit: sync
	PORT=4301 FORGE_MODELS_URL=http://forge-models:4300 FORGE_LOG_LEVEL=error uv run pytest -q

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ready"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["service"]=="forge-agents"; assert d["language"]=="python"; assert d["status"]=="running"; assert "version" in d'
	@# Agent registry (15.02)
	@curl -fsS "http://127.0.0.1:$(HOST_PORT)/v1/agents" | grep -q 'fixture-echo'
	@code="$$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/v1/agents/nope")"; test "$$code" = "404"
	@# Tool registry (15.03) + platform tools (15.05)
	@curl -fsS "http://127.0.0.1:$(HOST_PORT)/v1/tools" | grep -q '"required_permissions"'
	@curl -fsS "http://127.0.0.1:$(HOST_PORT)/v1/tools" | grep -q 'echo.ping'
	@curl -fsS "http://127.0.0.1:$(HOST_PORT)/v1/tools" | python3 -c 'import sys,json; tools=json.load(sys.stdin)["tools"]; assert any(t["name"]=="runtime.restart" and t["destructive"] for t in tools); assert any(t["name"]=="deployment.read" for t in tools); assert any(t["name"]=="logs.search" for t in tools)'
	@# Run engine dry-run (15.04)
	@rid="$$(curl -fsS -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/agents/fixture-echo/runs" \
		-H 'content-type: application/json' -H 'X-Forge-Project: integ' \
		-d '{"input":"hello","context":{"dry_run":true}}' \
		| python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])')"; \
		test -n "$$rid"; \
		body=""; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
			body="$$(curl -fsS "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$rid" -H 'X-Forge-Project: integ')"; \
			if echo "$$body" | grep -Eq '"status":"(succeeded|failed|stopped|cancelled)"'; then break; fi; \
			sleep 0.2; \
		done; \
		echo "$$body" | grep -q '"steps"'; \
		echo "$$body" | grep -q '"status":"succeeded"'; \
		code="$$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$rid" -H 'X-Forge-Project: other')"; \
		test "$$code" = "404"
	@# Structured JSON logs + request IDs
	@curl -sf -H 'X-Request-ID: integ-req-1' "http://127.0.0.1:$(HOST_PORT)/health/live" >/dev/null
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-agents"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-agents"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"request_id":"integ-req-1"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"agents_registry_size"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"tools_registry_size"'
	@grep -q '4301' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-agents.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p; assert "forge-agents" in p; assert "/v1/agents" in p; assert "Agent" in p; assert "agent_not_found" in p; assert "/v1/tools" in p; assert "ToolInvokeDenialReason" in p; assert "unknown_tool" in p; assert "/v1/runs" in p; assert "RunStep" in p; assert "max_steps_exceeded" in p'
	@# Invalid MODELS_URL fails process startup (ephemeral container).
	@docker rm -f forge-agents-bad-url >/dev/null 2>&1 || true
	@if docker run --name forge-agents-bad-url \
		-e PORT=8080 \
		-e FORGE_MODELS_URL=not-a-url \
		$(IMAGE) >/tmp/forge-agents-bad-url.out 2>&1; then \
		echo "expected non-zero exit for invalid FORGE_MODELS_URL" >&2; \
		docker rm -f forge-agents-bad-url >/dev/null 2>&1 || true; \
		exit 1; \
	fi
	@docker rm -f forge-agents-bad-url >/dev/null 2>&1 || true
	@grep -Eiq 'forge_models_url|FORGE_MODELS_URL|validation error|absolute http' /tmp/forge-agents-bad-url.out
	@# Invalid PORT fails startup.
	@docker rm -f forge-agents-bad-port >/dev/null 2>&1 || true
	@if docker run --name forge-agents-bad-port \
		-e PORT=not-a-port \
		-e FORGE_MODELS_URL=http://forge-models:4300 \
		$(IMAGE) >/tmp/forge-agents-bad-port.out 2>&1; then \
		echo "expected non-zero exit for invalid PORT" >&2; \
		docker rm -f forge-agents-bad-port >/dev/null 2>&1 || true; \
		exit 1; \
	fi
	@docker rm -f forge-agents-bad-port >/dev/null 2>&1 || true
	@grep -Eiq 'PORT|port|validation error' /tmp/forge-agents-bad-port.out
	@# Malformed agent YAML fails startup.
	@docker rm -f forge-agents-bad-agents >/dev/null 2>&1 || true
	@mkdir -p /tmp/forge-agents-bad-defs
	@printf 'name: [\n' > /tmp/forge-agents-bad-defs/bad.yaml
	@if docker run --name forge-agents-bad-agents \
		-e PORT=8080 \
		-e FORGE_MODELS_URL=http://forge-models:4300 \
		-e FORGE_AGENTS_DEFS_DIR=/bad-agents \
		-v /tmp/forge-agents-bad-defs:/bad-agents:ro \
		$(IMAGE) >/tmp/forge-agents-bad-agents.out 2>&1; then \
		echo "expected non-zero exit for malformed agent YAML" >&2; \
		docker rm -f forge-agents-bad-agents >/dev/null 2>&1 || true; \
		exit 1; \
	fi
	@docker rm -f forge-agents-bad-agents >/dev/null 2>&1 || true
	@grep -Eiq 'malformed|agent|registry|bad.yaml' /tmp/forge-agents-bad-agents.out
	@# SIGTERM → graceful exit 0 within grace period.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health, identity, agent/tool registry, run dry-run, JSON logs/request IDs, OpenAPI, bad URL/PORT/YAML, SIGTERM)."

lint: sync
	uv run ruff check app tests
	uv run ruff format --check app tests

format: sync
	uv run ruff format app tests

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	@rm -rf .venv .pytest_cache .ruff_cache __pycache__ app/__pycache__ tests/__pycache__
	@rm -f /tmp/forge-agents-bad-url.out /tmp/forge-agents-bad-port.out /tmp/forge-agents-bad-agents.out
	@rm -rf /tmp/forge-agents-bad-defs
	@find . -type d -name '__pycache__' -prune -exec rm -rf {} +
