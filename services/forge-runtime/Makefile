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
	FORGE_PROBE_INTERVAL_SECONDS=$${FORGE_PROBE_INTERVAL_SECONDS:-5} \
	FORGE_PROBE_TIMEOUT_SECONDS=$${FORGE_PROBE_TIMEOUT_SECONDS:-2} \
	FORGE_PROBE_FAILURE_THRESHOLD=$${FORGE_PROBE_FAILURE_THRESHOLD:-3} \
	FORGE_PROBE_HOST=$${FORGE_PROBE_HOST:-127.0.0.1} \
	FORGE_LOG_DEFAULT_TAIL=$${FORGE_LOG_DEFAULT_TAIL:-100} \
	FORGE_LOG_STREAM_BUFFER=$${FORGE_LOG_STREAM_BUFFER:-8192} \
	FORGE_STOP_GRACE_SECONDS=$${FORGE_STOP_GRACE_SECONDS:-10} \
	FORGE_ON_CONFIG_CONFLICT=$${FORGE_ON_CONFIG_CONFLICT:-recreate} \
	FORGE_CONTROL_URL=$${FORGE_CONTROL_URL:-} \
	FORGE_RECONCILE_INTERVAL_SECONDS=$${FORGE_RECONCILE_INTERVAL_SECONDS:-10} \
	FORGE_CONTROL_REPORT_MODE=$${FORGE_CONTROL_REPORT_MODE:-push} \
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

# Disable Control reconcile for direct /v1/workloads tests (otherwise orphans are deleted).
# The Control converge section below re-enables FORGE_CONTROL_URL with a short interval.
test-integration:
	@FORGE_CONTROL_URL= $(MAKE) run
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
	@# Health probing: wait until status reaches ready (Go demo implements /health/ready).
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do \
		st=$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/status" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
		if [ "$$st" = "ready" ]; then break; fi; \
		sleep 2; \
	done; \
	curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/status" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["deploymentId"]=="deployment-123"; assert d["status"]=="ready"; assert d["lastProbe"]["live"] is True; assert d["lastProbe"]["ready"] is True; assert "since" in d; assert "restarts" in d'
	@# Kill container out-of-band → status becomes failed (not ready).
	@docker stop forge-deployment-123 >/dev/null
	@sleep 6
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/status" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"] in ("failed","unhealthy","stopped"); assert d["status"]!="ready"'
	@docker rm -f forge-deployment-123 >/dev/null 2>&1 || true
	@# Recreate workload, restart Runtime, confirm rediscovery + probing resumes.
	@curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/workloads" \
		-H 'content-type: application/json' \
		-d '{"deployment_id":"deployment-123","image":"localhost:5000/demo-go:latest","port":8080,"environment":{"FORGE_ENV":"development","PORT":"8080"}}' \
		| python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["hostPort"]>0'
	@docker restart $(SERVICE) >/dev/null
	@$(MAKE) wait
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do \
		st=$$(curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/status" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
		if [ "$$st" = "ready" ]; then break; fi; \
		sleep 2; \
	done; \
	curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/status" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="ready", d'
	@# Log streaming: bounded fetch returns recent lines; unknown id is 404.
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/logs?tail=10" | grep -E '\[(stdout|stderr)\]'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/logs?format=ndjson&tail=5" | python3 -c 'import sys,json; lines=[json.loads(l) for l in sys.stdin if l.strip()]; assert lines; assert lines[0]["stream"] in ("stdout","stderr"); assert "message" in lines[0]'
	@code=$$(curl -s -o /tmp/forge-runtime-logs-404.json -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/v1/workloads/no-such-workload/logs?tail=1"); \
		test "$$code" = "404"
	@# follow=true streams SSE events; disconnect promptly (max-time).
	@curl -sf -N --max-time 3 "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123/logs?follow=true" | grep -q '^data:'
	@# Pull failure leaves no dangling container.
	@-docker rm -f forge-deployment-bogus >/dev/null 2>&1 || true
	@code=$$(curl -s -o /tmp/forge-runtime-pull-err.json -w '%{http_code}' -X POST "http://127.0.0.1:$(HOST_PORT)/v1/workloads" \
		-H 'content-type: application/json' \
		-d '{"deployment_id":"deployment-bogus","image":"localhost:5000/definitely-missing:latest","port":8080,"environment":{}}'); \
		test "$$code" = "502" -o "$$code" = "422" -o "$$code" = "500"; \
		! docker ps -a --format '{{.Names}}' | grep -qx 'forge-deployment-bogus'
	@# Idempotent create: second POST returns 200 and never duplicates the container.
	@code=$$(curl -s -o /tmp/forge-runtime-idempotent.json -w '%{http_code}' -X POST "http://127.0.0.1:$(HOST_PORT)/v1/workloads" \
		-H 'content-type: application/json' \
		-d '{"deployment_id":"deployment-123","image":"localhost:5000/demo-go:latest","port":8080,"environment":{"FORGE_ENV":"development","PORT":"8080"}}'); \
		test "$$code" = "200"; \
		count=$$(docker ps -aq --filter label=forge.deployment_id=deployment-123 | wc -l | tr -d ' '); \
		test "$$count" = "1"
	@# Foreign (unmanaged) containers are never stopped/removed by DELETE.
	@-docker rm -f forge-runtime-foreign-probe >/dev/null 2>&1 || true
	@docker run -d --name forge-runtime-foreign-probe --label com.example.foreign=true alpine:3.20 sleep 3600 >/dev/null
	@curl -sf -o /dev/null -w '%{http_code}' -X DELETE "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123" | grep -q 204
	@docker inspect forge-runtime-foreign-probe >/dev/null
	@docker rm -f forge-runtime-foreign-probe >/dev/null 2>&1 || true
	@# DELETE removes the managed container; second DELETE is still 204.
	@! docker ps -aq --filter label=forge.deployment_id=deployment-123 --filter label=forge.managed=true | grep -q .
	@curl -sf -o /dev/null -w '%{http_code}' -X DELETE "http://127.0.0.1:$(HOST_PORT)/v1/workloads/deployment-123" | grep -q 204
	@# Control desired→actual: create deployment → Runtime converges → /v1/node/state.
	@FORGE_CONTROL_URL=http://forge-control:8080 FORGE_RECONCILE_INTERVAL_SECONDS=3 $(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:4001/health/ready 90
	@SLUG="runtime-int-$$(date +%s)"; \
		PID=$$(curl -sf -X POST http://127.0.0.1:4001/v1/projects -H 'content-type: application/json' \
		-d "{\"name\":\"$$SLUG\",\"slug\":\"$$SLUG\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])'); \
		EID=$$(curl -sf -X POST http://127.0.0.1:4001/v1/projects/$$PID/environments -H 'content-type: application/json' \
			-d '{"name":"development"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])'); \
		AID=$$(curl -sf -X POST http://127.0.0.1:4001/v1/projects/$$PID/applications -H 'content-type: application/json' \
			-d '{"name":"api"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])'); \
		SID=$$(curl -sf -X POST http://127.0.0.1:4001/v1/applications/$$AID/services -H 'content-type: application/json' \
			-d '{"name":"web","port":8080}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])'); \
		DID=$$(curl -sf -X POST http://127.0.0.1:4001/v1/services/$$SID/deployments -H 'content-type: application/json' \
			-d "{\"image\":\"localhost:5000/demo-go:latest\",\"desiredReplicas\":1,\"environmentId\":\"$$EID\"}" \
			| python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])'); \
		echo "created deployment $$DID"; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do \
			if docker ps --filter label=forge.deployment_id=$$DID --filter label=forge.managed=true -q | grep -q .; then break; fi; \
			sleep 2; \
		done; \
		docker ps --filter label=forge.deployment_id=$$DID --filter label=forge.managed=true -q | grep -q .; \
		curl -sf http://127.0.0.1:$(HOST_PORT)/v1/node/state | DID="$$DID" python3 -c 'import sys,json,os; d=json.load(sys.stdin); assert d["nodeId"]; assert any(w["deploymentId"]==os.environ["DID"] for w in d["workloads"]), d'; \
		test -n "$$DID"; \
		docker exec forge-postgres psql -U forge -d forge -v ON_ERROR_STOP=1 -c "DELETE FROM control.deployments WHERE id = '$$DID'"; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do \
			if ! docker ps -aq --filter label=forge.deployment_id=$$DID --filter label=forge.managed=true | grep -q .; then break; fi; \
			sleep 2; \
		done; \
		! docker ps -aq --filter label=forge.deployment_id=$$DID --filter label=forge.managed=true | grep -q .
	@# Control down for a cycle: stop Control, confirm Runtime stays up and does not churn.
	@EXISTING=$$(docker ps -aq --filter label=forge.managed=true | wc -l | tr -d ' '); \
		$(COMPOSE) stop forge-control >/dev/null; \
		sleep 12; \
		curl -sf http://127.0.0.1:$(HOST_PORT)/health/live | grep -q '"status":"ok"'; \
		AFTER=$$(docker ps -aq --filter label=forge.managed=true | wc -l | tr -d ' '); \
		test "$$EXISTING" = "$$AFTER"; \
		$(COMPOSE) start forge-control >/dev/null; \
		$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:4001/health/ready 90
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health + node + workloads + status + logs + idempotent create/delete + control converge + graceful SIGTERM)."

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
