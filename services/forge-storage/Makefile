SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-storage
IMAGE := forge/forge-storage:local
HOST_PORT ?= 4107

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	@mkdir -p .tmp/forge-storage
	PORT=$${PORT:-4107} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-storage} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_STORAGE_ROOT=$${FORGE_STORAGE_ROOT:-$(CURDIR)/.tmp/forge-storage} \
	FORGE_STORAGE_ALLOWED_BASE=$${FORGE_STORAGE_ALLOWED_BASE:-$(CURDIR)/.tmp} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
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
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["service"]=="forge-storage"; assert d["language"]=="rust"; assert d["status"]=="running"; assert "version" in d'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-storage"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-storage"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'local filesystem storage root ready'
	@# Durability: objects/ + meta/ survive restart.
	@$(COMPOSE) exec -T $(SERVICE) sh -c 'test -d /data/storage/objects && test -d /data/storage/meta'
	@$(COMPOSE) exec -T $(SERVICE) sh -c 'echo durable > /data/storage/objects/.probe && test -f /data/storage/objects/.probe'
	@docker restart $(SERVICE) >/dev/null
	@$(MAKE) wait
	@$(COMPOSE) exec -T $(SERVICE) sh -c 'test -f /data/storage/objects/.probe && test -d /data/storage/meta'
	@# Read-only root → readiness 503 (ephemeral container).
	@$(COMPOSE) stop $(SERVICE) >/dev/null
	@docker rm -f forge-storage-ro >/dev/null 2>&1 || true
	@docker run -d --name forge-storage-ro -p $(HOST_PORT):8080 \
		-e PORT=8080 \
		-e FORGE_SERVICE_NAME=forge-storage \
		-e FORGE_STORAGE_ROOT=/ro-data \
		-e FORGE_STORAGE_ALLOWED_BASE=/ro-data \
		--tmpfs /ro-data:ro,size=1m \
		$(IMAGE) >/dev/null
	@for i in $$(seq 1 45); do \
		code=$$(curl -s -o /tmp/forge-storage-ro-live.json -w '%{http_code}' \
			"http://127.0.0.1:$(HOST_PORT)/health/live" || true); \
		if [[ "$$code" == "200" ]]; then break; fi; \
		sleep 1; \
	done
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@code=$$(curl -s -o /tmp/forge-storage-ro-ready.json -w '%{http_code}' \
		"http://127.0.0.1:$(HOST_PORT)/health/ready"); \
		test "$$code" = "503"; \
		grep -q '"status":"not_ready"' /tmp/forge-storage-ro-ready.json
	@docker rm -f forge-storage-ro >/dev/null 2>&1 || true
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@grep -q '4107' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-storage.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p; assert "forge-storage" in p; assert "/v1/buckets" in p; assert "CreateBucketRequest" in p; assert "X-Forge-Project" in p'
	@# Bucket lifecycle + project isolation (13.02).
	@curl -s -XDELETE "http://127.0.0.1:$(HOST_PORT)/v1/buckets/artifacts" -H 'X-Forge-Project: proj-a' >/dev/null || true
	@curl -fsS -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/buckets" \
		-H 'X-Forge-Project: proj-a' -H 'content-type: application/json' \
		-d '{"name":"artifacts"}' | grep -q '"name":"artifacts"'
	@curl -fsS "http://127.0.0.1:$(HOST_PORT)/v1/buckets" -H 'X-Forge-Project: proj-a' | grep -q artifacts
	@test "$$(curl -fsS "http://127.0.0.1:$(HOST_PORT)/v1/buckets" -H 'X-Forge-Project: proj-b' | grep -c artifacts)" = "0"
	@code=$$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/v1/buckets/artifacts" -H 'X-Forge-Project: proj-b'); \
		test "$$code" = "404"
	@curl -fsS -XDELETE "http://127.0.0.1:$(HOST_PORT)/v1/buckets/artifacts" -H 'X-Forge-Project: proj-a' -o /dev/null -w '%{http_code}\n' | grep -q 204
	@curl -fsS -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/buckets" \
		-H 'X-Forge-Project: proj-a' -H 'content-type: application/json' \
		-d '{"name":"artifacts"}' | grep -q '"name":"artifacts"'
	@$(COMPOSE) exec -T $(SERVICE) sh -c 'test -f /data/storage/meta/index.db'
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@! PORT=not-a-port FORGE_STORAGE_ROOT=/tmp/x cargo run --quiet >/tmp/forge-storage-bad-port.out 2>&1
	@grep -q 'PORT' /tmp/forge-storage-bad-port.out
	@echo "Integration checks passed (health, identity, FS layout, buckets/isolation, restart durability, RO→503, OpenAPI, SIGTERM)."

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
	@rm -f /tmp/forge-storage-ro-live.json /tmp/forge-storage-ro-ready.json /tmp/forge-storage-bad-port.out
