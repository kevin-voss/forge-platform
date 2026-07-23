SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-workflows
IMAGE := forge/forge-workflows:local
HOST_PORT ?= 4302
ELIXIR_IMAGE ?= hexpm/elixir:1.18.4-erlang-27.3.4-debian-bookworm-20250610-slim

.PHONY: compile test test-unit test-integration run wait docker-build docker-run clean dev

compile:
	@docker run --rm \
		-v "$(CURDIR):/app" -w /app \
		-e MIX_ENV=dev \
		$(ELIXIR_IMAGE) \
		bash -lc 'mix local.hex --force && mix local.rebar --force && mix deps.get && mix compile'

dev: docker-build
	@docker run --rm -p $(HOST_PORT):8080 \
		-e PORT=8080 \
		-e FORGE_SERVICE_NAME=forge-workflows \
		-e FORGE_SERVICE_VERSION=0.1.0 \
		-e FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
		-e FORGE_ENV=$${FORGE_ENV:-development} \
		-e FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
		$(IMAGE)

docker-build:
	docker build -t $(IMAGE) .

run: docker-build
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit: docker-build
	@echo "Unit tests passed during image build"

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ready"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["service"]=="forge-workflows"; assert d["language"]=="elixir"; assert d["status"]=="running"; assert "version" in d'
	@# Structured JSON logs + request IDs + supervision tree
	@curl -sf -H 'X-Request-ID: integ-req-1' "http://127.0.0.1:$(HOST_PORT)/health/live" >/dev/null
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-workflows"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-workflows"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"supervision"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"request_id":"integ-req-1"'
	@grep -q '4302' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-workflows.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p; assert "forge-workflows" in p; assert "elixir" in p; assert "/v1/workflows" not in p; assert "/v1/runs" not in p'
	@$(MAKE) -C $(ROOT_DIR) lint-openapi
	@# Invalid PORT fails startup.
	@docker rm -f forge-workflows-bad-port >/dev/null 2>&1 || true
	@if docker run --name forge-workflows-bad-port \
		-e PORT=not-a-port \
		$(IMAGE) >/tmp/forge-workflows-bad-port.out 2>&1; then \
		echo "expected non-zero exit for invalid PORT" >&2; \
		docker rm -f forge-workflows-bad-port >/dev/null 2>&1 || true; \
		exit 1; \
	fi
	@docker rm -f forge-workflows-bad-port >/dev/null 2>&1 || true
	@grep -Eiq 'PORT|port' /tmp/forge-workflows-bad-port.out
	@# SIGTERM → graceful exit 0 within grace period.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health, identity, JSON logs/request IDs, supervision, OpenAPI, bad PORT, SIGTERM)."

docker-run: run

clean:
	@rm -rf _build deps .elixir_ls
	@rm -f /tmp/forge-workflows-bad-port.out
