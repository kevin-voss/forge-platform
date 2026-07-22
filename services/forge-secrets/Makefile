SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-secrets
IMAGE := forge/forge-secrets:local
HOST_PORT ?= 4104
LOCAL_MASTER_KEY ?= bG9jYWwtZGV2LWZvcmdlLXNlY3JldHMta2V5ISEhISE=

.PHONY: ensure-db dev build run test test-unit test-integration lint format docker-build docker-run clean wait

ensure-db:
	@$(COMPOSE) up -d postgres
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:5003/healthz 90 >/dev/null 2>&1 || true
	@for i in $$(seq 1 60); do \
		if $(COMPOSE) exec -T postgres pg_isready -U forge -d forge >/dev/null 2>&1; then break; fi; \
		sleep 1; \
	done
	@if ! $(COMPOSE) exec -T postgres psql -U forge -d forge -tAc \
		"SELECT 1 FROM pg_database WHERE datname='forge_secrets'" | grep -qx 1; then \
		$(COMPOSE) exec -T postgres psql -U forge -d forge -v ON_ERROR_STOP=1 -c 'CREATE DATABASE forge_secrets'; \
	fi

dev: ensure-db
	PORT=$${PORT:-4104} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-secrets} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_SECRETS_DB_URL=$${FORGE_SECRETS_DB_URL:-postgres://forge:forge@127.0.0.1:5001/forge_secrets} \
	FORGE_SECRETS_MASTER_KEY=$${FORGE_SECRETS_MASTER_KEY:-$(LOCAL_MASTER_KEY)} \
	FORGE_SECRETS_MASTER_KEY_ID=$${FORGE_SECRETS_MASTER_KEY_ID:-m1} \
	cargo run

build:
	cargo build --release

run: docker-build ensure-db
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
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["service"]=="forge-secrets"; assert d["language"]=="rust"; assert d["status"]=="running"'
	@# Generate a project data key → wrapped row persisted (metadata only).
	@PROJ="proj-10-01-$$(date +%s)"; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/projects/$${PROJ}/data-keys" \
			| python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["project_id"]; assert d["key_version"]==1; assert d["master_key_id"]; assert d["plaintext_returned"] is False; assert d["created"] is True'; \
		curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/projects/$${PROJ}/data-keys" \
			| python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["plaintext_returned"] is False'; \
		$(COMPOSE) exec -T postgres psql -U forge -d forge_secrets -tAc \
			"SELECT COUNT(*) FROM project_data_keys WHERE project_id='$${PROJ}'" | grep -qx '1'; \
		echo "$${PROJ}" > /tmp/forge-secrets-int-project
	@# Restart → wrapped keys reload and unwrap correctly.
	@PROJ="$$(cat /tmp/forge-secrets-int-project)"; \
		docker restart $(SERVICE) >/dev/null; \
		$(MAKE) wait; \
		curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/projects/$${PROJ}/data-keys" \
			| python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["project_id"]; assert d["plaintext_returned"] is False'; \
		code=$$(curl -s -o /tmp/forge-secrets-idempotent.json -w '%{http_code}' -X POST \
			"http://127.0.0.1:$(HOST_PORT)/v1/projects/$${PROJ}/data-keys"); \
		test "$$code" = "200"; \
		python3 -c 'import json; d=json.load(open("/tmp/forge-secrets-idempotent.json")); assert d["created"] is False'
	@# Missing master key → ready non-200; data-key ops refused.
	@$(COMPOSE) stop $(SERVICE) >/dev/null
	@docker rm -f forge-secrets-nokey >/dev/null 2>&1 || true
	@NET=$$(docker inspect -f '{{range $$k,$$v := .NetworkSettings.Networks}}{{$$k}}{{end}}' forge-postgres); \
		test -n "$$NET"; \
		docker run -d --name forge-secrets-nokey --network "$$NET" -p $(HOST_PORT):8080 \
			-e PORT=8080 \
			-e FORGE_SERVICE_NAME=forge-secrets \
			-e FORGE_SECRETS_DB_URL=postgres://forge:forge@postgres:5432/forge_secrets \
			-e FORGE_SECRETS_MASTER_KEY_ID=m1 \
			$(IMAGE) >/dev/null
	@for i in $$(seq 1 45); do \
		code=$$(curl -s -o /tmp/forge-secrets-nokey-ready.json -w '%{http_code}' \
			"http://127.0.0.1:$(HOST_PORT)/health/live" || true); \
		if [[ "$$code" == "200" ]]; then break; fi; \
		sleep 1; \
	done
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@code=$$(curl -s -o /tmp/forge-secrets-nokey-ready.json -w '%{http_code}' \
		"http://127.0.0.1:$(HOST_PORT)/health/ready"); \
		test "$$code" = "503"
	@code=$$(curl -s -o /tmp/forge-secrets-nokey-op.json -w '%{http_code}' -X POST \
		"http://127.0.0.1:$(HOST_PORT)/v1/projects/refused/data-keys"); \
		test "$$code" = "503"
	@docker rm -f forge-secrets-nokey >/dev/null 2>&1 || true
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@! $(COMPOSE) logs $(SERVICE) 2>&1 | grep -E 'bG9jYWwtZGV2LWZvcmdl|local-dev-forge-secrets-key'
	@grep -q '4104' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@# OpenAPI skeleton declares the three contract paths.
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-secrets.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p'
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health, identity, data-key wrap/persist/restart, missing-key refusal, OpenAPI, SIGTERM)."

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
