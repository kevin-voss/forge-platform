SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-build
IMAGE := forge/forge-build:local
HOST_PORT ?= 4103
LOCAL_WORKSPACE ?= $(ROOT_DIR)/services/forge-build/.tmp/workspace
FIXTURE_DIR := $(ROOT_DIR)/services/forge-build/testdata/app

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait prepare-fixture

dev:
	@mkdir -p "$(LOCAL_WORKSPACE)"
	PORT=$${PORT:-8080} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-build} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_AUTH_MODE=$${FORGE_AUTH_MODE:-dev} \
	DOCKER_HOST=$${DOCKER_HOST:-unix:///var/run/docker.sock} \
	FORGE_BUILD_WORKSPACE_DIR=$${FORGE_BUILD_WORKSPACE_DIR:-$(LOCAL_WORKSPACE)} \
	FORGE_DEFAULT_FORGE_YAML=$${FORGE_DEFAULT_FORGE_YAML:-forge.yaml} \
	FORGE_BUILD_TIMEOUT_SECONDS=$${FORGE_BUILD_TIMEOUT_SECONDS:-600} \
	FORGE_BUILD_MAX_CONCURRENCY=$${FORGE_BUILD_MAX_CONCURRENCY:-2} \
	FORGE_BUILD_LOG_BUFFER_LINES=$${FORGE_BUILD_LOG_BUFFER_LINES:-5000} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_DOCKER_STARTUP_RETRIES=$${FORGE_DOCKER_STARTUP_RETRIES:-5} \
	FORGE_DOCKER_STARTUP_RETRY_DELAY_MS=$${FORGE_DOCKER_STARTUP_RETRY_DELAY_MS:-500} \
	go run .

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-build .

prepare-fixture:
	@test -f "$(FIXTURE_DIR)/Dockerfile" && test -f "$(FIXTURE_DIR)/forge.yaml"
	@if [ ! -d "$(FIXTURE_DIR)/.git" ]; then \
		git -C "$(FIXTURE_DIR)" init -b main >/dev/null; \
		git -C "$(FIXTURE_DIR)" config user.email "forge@local"; \
		git -C "$(FIXTURE_DIR)" config user.name "forge"; \
		git -C "$(FIXTURE_DIR)" add Dockerfile forge.yaml; \
		git -C "$(FIXTURE_DIR)" commit -m "fixture" >/dev/null; \
	fi

run: prepare-fixture docker-build
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
	@# Confirm structured JSON startup logs (Docker version + workspace).
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-build"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-build"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'docker_engine_version'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'workspace_dir'
	@# Clone → docker build → streamed logs against the mounted fixture.
	@BID="$$(curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/builds" \
		-H 'content-type: application/json' \
		-d '{"repo":"file:///fixtures/app","ref":"main","forgeYamlPath":"forge.yaml"}' \
		| python3 -c 'import sys,json; print(json.load(sys.stdin)["buildId"])')"; \
		test -n "$$BID"; \
		STATUS=""; \
		for i in $$(seq 1 90); do \
			BODY="$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/builds/$$BID")"; \
			STATUS="$$(printf '%s' "$$BODY" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])')"; \
			if [ "$$STATUS" = "succeeded" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 1; \
		done; \
		if [ "$$STATUS" != "succeeded" ]; then \
			echo "build $$BID did not succeed: $$BODY" >&2; \
			curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/builds/$$BID/logs" >&2 || true; \
			exit 1; \
		fi; \
		LOGS="$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/builds/$$BID/logs")"; \
		echo "$$LOGS" | grep -q 'checked out commit'; \
		echo "$$LOGS" | grep -q 'build succeeded'; \
		IMG="$$(printf '%s' "$$BODY" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("image",""))')"; \
		test -n "$$IMG"; \
		docker image inspect "$$IMG" >/dev/null; \
		echo "Build $$BID succeeded with image $$IMG"
	@# Invalid PORT fails startup (config validation).
	@mkdir -p "$(LOCAL_WORKSPACE)"
	@! PORT=not-a-port FORGE_BUILD_WORKSPACE_DIR="$(LOCAL_WORKSPACE)" go run . >/tmp/forge-build-bad-port.out 2>&1
	@grep -q 'PORT' /tmp/forge-build-bad-port.out
	@# Bad Docker socket → readiness 503 (live still 200).
	@docker run --rm -d --name forge-build-bad-socket-$$$$ \
		-e PORT=8080 \
		-e FORGE_SERVICE_NAME=forge-build \
		-e DOCKER_HOST=unix:///no/such/docker.sock \
		-e FORGE_BUILD_WORKSPACE_DIR=/workspace \
		-e FORGE_DOCKER_STARTUP_RETRIES=0 \
		-p 14103:8080 \
		$(IMAGE) >/tmp/forge-build-bad-socket.cid
	@cid="$$(cat /tmp/forge-build-bad-socket.cid)"; \
		trap 'docker rm -f "$$cid" >/dev/null 2>&1 || true' EXIT; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
			if curl -sf "http://127.0.0.1:14103/health/live" | grep -q '"status":"ok"'; then break; fi; \
			sleep 1; \
		done; \
		curl -sf "http://127.0.0.1:14103/health/live" | grep -q '"status":"ok"'; \
		code="$$(curl -s -o /tmp/forge-build-ready-503.json -w '%{http_code}' "http://127.0.0.1:14103/health/ready")"; \
		test "$$code" = "503"; \
		grep -q '"status":"not_ready"' /tmp/forge-build-ready-503.json; \
		docker rm -f "$$cid" >/dev/null
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (build+logs + health + docker ready/503 + structured logs + invalid PORT + graceful SIGTERM)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-build-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	rm -rf bin .tmp
	@rm -f /tmp/forge-build-bad-port.out /tmp/forge-build-gofmt.out \
		/tmp/forge-build-bad-socket.cid /tmp/forge-build-ready-503.json
