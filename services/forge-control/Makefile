SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-control
IMAGE := forge/forge-control:local
HOST_PORT ?= 4001

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait migrate

dev:
	PORT=$${PORT:-8080} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-control} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	DATABASE_URL=$${DATABASE_URL:-jdbc:postgresql://127.0.0.1:5001/forge} \
	DATABASE_USER=$${DATABASE_USER:-forge} \
	DATABASE_PASSWORD=$${DATABASE_PASSWORD:-forge} \
	DATABASE_SCHEMA=$${DATABASE_SCHEMA:-control} \
	DATABASE_POOL_MAX=$${DATABASE_POOL_MAX:-10} \
	DATABASE_MIGRATE_ON_START=$${DATABASE_MIGRATE_ON_START:-true} \
	./gradlew --quiet run

migrate:
	PORT=$${PORT:-8080} \
	DATABASE_URL=$${DATABASE_URL:-jdbc:postgresql://127.0.0.1:5001/forge} \
	DATABASE_USER=$${DATABASE_USER:-forge} \
	DATABASE_PASSWORD=$${DATABASE_PASSWORD:-forge} \
	DATABASE_SCHEMA=$${DATABASE_SCHEMA:-control} \
	DATABASE_POOL_MAX=$${DATABASE_POOL_MAX:-10} \
	./gradlew --quiet migrate

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
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@echo "Integration checks passed (migrations, repos, health + graceful SIGTERM)."

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
