SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-memory
IMAGE := forge/forge-memory:local
HOST_PORT ?= 4303

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	@mkdir -p .tmp/forge-memory
	PORT=$${PORT:-4303} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-memory} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_MEMORY_ROOT=$${FORGE_MEMORY_ROOT:-$(CURDIR)/.tmp/forge-memory} \
	FORGE_MEMORY_ALLOWED_BASE=$${FORGE_MEMORY_ALLOWED_BASE:-$(CURDIR)/.tmp} \
	cargo run

build:
	cargo build --release

run: docker-build
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit:
	cargo test --quiet

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ready"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["service"]=="forge-memory"; assert d["language"]=="rust"; assert d["status"]=="running"; assert "version" in d'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-memory"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-memory"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'local filesystem memory root ready'
	@# Durability: vectors/ + meta/ survive restart.
	@$(COMPOSE) exec -T $(SERVICE) sh -c 'test -d /data/memory/vectors && test -d /data/memory/meta'
	@$(COMPOSE) exec -T $(SERVICE) sh -c 'echo durable > /data/memory/vectors/.probe && test -f /data/memory/vectors/.probe'
	@docker restart $(SERVICE) >/dev/null
	@$(MAKE) wait
	@$(COMPOSE) exec -T $(SERVICE) sh -c 'test -f /data/memory/vectors/.probe && test -d /data/memory/meta'
	@# Read-only root → readiness 503 (ephemeral container).
	@$(COMPOSE) stop $(SERVICE) >/dev/null
	@docker rm -f forge-memory-ro >/dev/null 2>&1 || true
	@docker run -d --name forge-memory-ro -p $(HOST_PORT):8080 \
		-e PORT=8080 \
		-e FORGE_SERVICE_NAME=forge-memory \
		-e FORGE_MEMORY_ROOT=/ro-data \
		-e FORGE_MEMORY_ALLOWED_BASE=/ro-data \
		--tmpfs /ro-data:ro,size=1m \
		$(IMAGE) >/dev/null
	@for i in $$(seq 1 45); do \
		code=$$(curl -s -o /tmp/forge-memory-ro-live.json -w '%{http_code}' \
			"http://127.0.0.1:$(HOST_PORT)/health/live" || true); \
		if [[ "$$code" == "200" ]]; then break; fi; \
		sleep 1; \
	done
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@code=$$(curl -s -o /tmp/forge-memory-ro-ready.json -w '%{http_code}' \
		"http://127.0.0.1:$(HOST_PORT)/health/ready"); \
		test "$$code" = "503"; \
		grep -q '"status":"not_ready"' /tmp/forge-memory-ro-ready.json
	@docker rm -f forge-memory-ro >/dev/null 2>&1 || true
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@grep -q '4303' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-memory.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p; assert "/v1/collections" in p; assert "/upsert" in p and "/query" in p; assert "dimension_mismatch" in p; assert "forge-memory" in p; paths=[l for l in p.splitlines() if l.startswith("  /")]; assert len(paths)>=8, paths'
	@BASE=http://127.0.0.1:$(HOST_PORT); P='-H X-Forge-Project:proj-a'; \
		curl -fsS $$P -XPOST $$BASE/v1/collections -H 'content-type: application/json' \
			-d '{"name":"incidents","dim":384,"distance":"cosine"}' | grep -q '"dim":384'; \
		curl -fsS $$P $$BASE/v1/collections/incidents | grep -q '"dim":384'; \
		code=$$(curl -s -o /tmp/forge-memory-dup.json -w '%{http_code}' $$P -XPOST $$BASE/v1/collections \
			-H 'content-type: application/json' -d '{"name":"incidents","dim":384,"distance":"cosine"}'); \
		test "$$code" = "409"; \
		grep -q '"code":"conflict"' /tmp/forge-memory-dup.json; \
		code=$$(curl -s -o /tmp/forge-memory-miss.json -w '%{http_code}' $$P $$BASE/v1/collections/missing); \
		test "$$code" = "404"
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@! PORT=not-a-port FORGE_MEMORY_ROOT=/tmp/x cargo run --quiet >/tmp/forge-memory-bad-port.out 2>&1
	@grep -q 'PORT' /tmp/forge-memory-bad-port.out
	@echo "Integration checks passed (health, identity, collections CRUD, FS layout, restart durability, RO→503, OpenAPI, SIGTERM)."

lint:
	@cargo fmt --check
	@cargo clippy --quiet -- -D warnings

format:
	cargo fmt

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	cargo clean
	@rm -rf .tmp
	@rm -f /tmp/forge-memory-ro-live.json /tmp/forge-memory-ro-ready.json /tmp/forge-memory-bad-port.out
