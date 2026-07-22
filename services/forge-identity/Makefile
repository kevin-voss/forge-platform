SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-identity
IMAGE := forge/forge-identity:local
HOST_PORT ?= 4002

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	PORT=$${PORT:-4002} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-identity} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_IDENTITY_DB_URL=$${FORGE_IDENTITY_DB_URL:-jdbc:postgresql://127.0.0.1:5001/forge_identity} \
	FORGE_IDENTITY_DB_USER=$${FORGE_IDENTITY_DB_USER:-forge} \
	FORGE_IDENTITY_DB_PASSWORD=$${FORGE_IDENTITY_DB_PASSWORD:-forge} \
	FORGE_IDENTITY_DB_POOL_MAX=$${FORGE_IDENTITY_DB_POOL_MAX:-10} \
	FORGE_IDENTITY_DB_MIGRATE_ON_START=$${FORGE_IDENTITY_DB_MIGRATE_ON_START:-true} \
	./gradlew --quiet run

build:
	./gradlew --quiet jar

run: docker-build
	@$(COMPOSE) up -d postgres
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit:
	./gradlew --quiet test

test-integration: run
	./gradlew --quiet integrationTest
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ready"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | grep -q '"service":"forge-identity"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | grep -q '"language":"kotlin"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | grep -q '"status":"running"'
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@echo "Integration checks passed (migrations, health, identity, graceful SIGTERM)."

lint:
	@echo "No Kotlin linter configured yet."

format:
	@echo "No Kotlin formatter configured yet."

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	./gradlew --quiet clean
	@rm -rf .tmp
