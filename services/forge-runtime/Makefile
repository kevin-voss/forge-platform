SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-runtime
IMAGE := forge/forge-runtime:local
HOST_PORT ?= 4102

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	PORT=$${PORT:-8080} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-runtime} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
	DOCKER_HOST=$${DOCKER_HOST:-unix:///var/run/docker.sock} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
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
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@# Confirm structured startup log recorded Docker Engine version.
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'docker_engine_version'
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health + docker ready + graceful SIGTERM)."

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
