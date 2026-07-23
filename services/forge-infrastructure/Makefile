SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-infrastructure
IMAGE := forge/forge-infrastructure:local
HOST_PORT ?= 4111

.PHONY: dev build run test test-unit test-integration lint format docker-build wait

dev:
	PORT=$${PORT:-8080} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-infrastructure} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_INFRA_DB_URL=$${FORGE_INFRA_DB_URL:-postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable} \
	FORGE_DATABASE_SCHEMA=$${FORGE_DATABASE_SCHEMA:-infrastructure} \
	FORGE_DATABASE_POOL_MAX=$${FORGE_DATABASE_POOL_MAX:-10} \
	FORGE_DATABASE_MIGRATE_ON_START=$${FORGE_DATABASE_MIGRATE_ON_START:-true} \
	FORGE_REGISTRY_URL=$${FORGE_REGISTRY_URL:-http://127.0.0.1:4001} \
	FORGE_CONTROL_URL=$${FORGE_CONTROL_URL:-http://127.0.0.1:4001} \
	FORGE_INFRA_RECONCILE_INTERVAL_MS=$${FORGE_INFRA_RECONCILE_INTERVAL_MS:-2000} \
	FORGE_INFRA_DOCKER_SOCKET=$${FORGE_INFRA_DOCKER_SOCKET:-/var/run/docker.sock} \
	FORGE_INFRA_DOCKER_NETWORK=$${FORGE_INFRA_DOCKER_NETWORK:-forge-platform_default} \
	FORGE_INFRA_DOCKER_IMAGE=$${FORGE_INFRA_DOCKER_IMAGE:-forge/forge-runtime:local} \
	FORGE_INFRA_DOCKER_HOST_ADDRESS=$${FORGE_INFRA_DOCKER_HOST_ADDRESS:-127.0.0.1} \
	FORGE_INFRA_ORPHAN_SCAN_INTERVAL_S=$${FORGE_INFRA_ORPHAN_SCAN_INTERVAL_S:-30} \
	go run ./cmd/forge-infrastructure

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-infrastructure ./cmd/forge-infrastructure

run: docker-build
	@FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} $(COMPOSE) up -d --build postgres forge-control $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 120
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 180

test: test-unit test-integration

test-unit:
	go test ./...

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"event":"infra.started"'
	@# ProviderNotConfigured path: unknown provider type + NodePool referencing it.
	@SUFFIX="$$RANDOM"; \
		PROV="itest-unknown-$$SUFFIX"; POOL="itest-pool-$$SUFFIX"; NK="$$POOL#crash"; \
		code=$$(curl -s -o /tmp/forge-infra-provider.json -w '%{http_code}' -X POST "http://127.0.0.1:4001/v1/infrastructureproviders" \
			-H 'content-type: application/json' \
			-d "{\"apiVersion\":\"forge.dev/v1\",\"kind\":\"InfrastructureProvider\",\"metadata\":{\"name\":\"$$PROV\"},\"spec\":{\"type\":\"unknown-provider\"}}"); \
		test "$$code" = "201" -o "$$code" = "200"; \
		grep -q "\"name\":\"$$PROV\"" /tmp/forge-infra-provider.json; \
		code=$$(curl -s -o /tmp/forge-infra-pool.json -w '%{http_code}' -X POST "http://127.0.0.1:4001/v1/nodepools" \
			-H 'content-type: application/json' \
			-d "{\"apiVersion\":\"forge.dev/v1\",\"kind\":\"NodePool\",\"metadata\":{\"name\":\"$$POOL\"},\"spec\":{\"providerRef\":\"$$PROV\",\"replicas\":1,\"machineType\":\"docker-small\",\"region\":\"local\"}}"); \
		test "$$code" = "201" -o "$$code" = "200"; \
		grep -q "\"name\":\"$$POOL\"" /tmp/forge-infra-pool.json; \
		ok=0; for i in $$(seq 1 30); do \
			body=$$(curl -sf "http://127.0.0.1:4001/v1/nodepools/$$POOL" || true); \
			echo "$$body" | grep -q 'ProviderNotConfigured' && ok=1 && break; \
			sleep 1; \
		done; \
		test "$$ok" = "1"; \
		docker exec forge-postgres psql -U forge -d forge -c \
			"INSERT INTO infrastructure.provider_operations (id, provider_name, kind, target_kind, natural_key, request, status) \
			 VALUES ('op_itestpending$$SUFFIX', '$$PROV', 'create_node', 'node', '$$NK', '{}'::jsonb, 'pending') \
			 ON CONFLICT DO NOTHING;" >/dev/null; \
		$(COMPOSE) restart $(SERVICE) >/dev/null; \
		$(MAKE) wait; \
		count=$$(docker exec forge-postgres psql -U forge -d forge -tAc \
			"SELECT COUNT(*) FROM infrastructure.provider_operations WHERE natural_key='$$NK'"); \
		test "$$count" = "1"
	@echo "Integration checks passed (health + ProviderNotConfigured + ledger idempotency)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-infra-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .
