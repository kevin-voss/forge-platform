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
	FORGE_RUNTIME_DATA_DIR=$${FORGE_RUNTIME_DATA_DIR:-.tmp/forge-runtime-data} \
	FORGE_HEARTBEAT_INTERVAL_SECONDS=$${FORGE_HEARTBEAT_INTERVAL_SECONDS:-10} \
	FORGE_PULL_TIMEOUT_SECONDS=$${FORGE_PULL_TIMEOUT_SECONDS:-120} \
	FORGE_DEFAULT_REGISTRY=$${FORGE_DEFAULT_REGISTRY:-localhost:5000} \
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
	@# Node identity + heartbeat.
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/node" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["id"]; assert "lastHeartbeat" in d'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/node/heartbeat" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["nodeId"]; assert d["healthy"] is True'
	@ID1=$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/node" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])'); \
		docker restart $(SERVICE) >/dev/null; \
		$(MAKE) wait; \
		ID2=$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/node" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])'); \
		test "$$ID1" = "$$ID2"
	@# Ensure Go demo image is in the local registry for workload create/start.
	@docker image inspect forge/demo-go-api:local >/dev/null 2>&1 || \
		docker build -t forge/demo-go-api:local $(ROOT_DIR)/demos/01-container-runtime/apps/go
	@docker tag forge/demo-go-api:local localhost:5000/demo-go:latest
	@docker push localhost:5000/demo-go:latest >/dev/null
	@# Clean any leftover test workload container.
	@-docker rm -f forge-deployment-123 >/dev/null 2>&1 || true
	@curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/workloads" \
		-H 'content-type: application/json' \
		-d '{"deployment_id":"deployment-123","image":"localhost:5000/demo-go:latest","port":8080,"environment":{"FORGE_ENV":"development","PORT":"8080"}}' \
		| python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["deploymentId"]=="deployment-123"; assert d["hostPort"]>0; assert d["state"]=="starting"; assert d["containerId"]'
	@HP=$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["hostPort"]>0; print(d["hostPort"])'); \
		$(ROOT_DIR)/scripts/wait-for-service.sh "http://127.0.0.1:$$HP/health/live" 60; \
		curl -sf "http://127.0.0.1:$$HP/health/live" | grep -q ok
	@NODE_ID=$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/node" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])'); \
		NODE_ID="$$NODE_ID" docker inspect forge-deployment-123 --format '{{json .Config.Labels}}' | \
		NODE_ID="$$NODE_ID" python3 -c 'import sys,json,os; d=json.load(sys.stdin); assert d.get("forge.deployment_id")=="deployment-123"; assert d.get("forge.managed")=="true"; assert d.get("forge.node_id")==os.environ["NODE_ID"]'
	@# Pull failure leaves no dangling container.
	@-docker rm -f forge-deployment-bogus >/dev/null 2>&1 || true
	@code=$$(curl -s -o /tmp/forge-runtime-pull-err.json -w '%{http_code}' -X POST "http://127.0.0.1:$(HOST_PORT)/v1/workloads" \
		-H 'content-type: application/json' \
		-d '{"deployment_id":"deployment-bogus","image":"localhost:5000/definitely-missing:latest","port":8080,"environment":{}}'); \
		test "$$code" = "502" -o "$$code" = "422" -o "$$code" = "500"; \
		! docker ps -a --format '{{.Names}}' | grep -qx 'forge-deployment-bogus'
	@docker rm -f forge-deployment-123 >/dev/null 2>&1 || true
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health + node + workloads create/start + graceful SIGTERM)."

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
