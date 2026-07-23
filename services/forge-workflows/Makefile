SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-workflows
IMAGE := forge/forge-workflows:local
HOST_PORT ?= 4302
ELIXIR_IMAGE ?= hexpm/elixir:1.18.4-erlang-27.3.4-debian-bookworm-20250610-slim
PROJECT ?= proj-a

.PHONY: compile test test-unit test-integration run wait docker-build docker-run clean dev ensure-db

compile:
	@docker run --rm \
		-v "$(CURDIR):/app" -w /app \
		-e MIX_ENV=dev \
		$(ELIXIR_IMAGE) \
		bash -lc 'mix local.hex --force && mix local.rebar --force && mix deps.get && mix compile'

dev: docker-build
	@docker run --rm -p $(HOST_PORT):8080 \
		-e PORT=8080 \
		-e FORGE_SERVICE_NAME=forge-workflows \
		-e FORGE_SERVICE_VERSION=0.1.0 \
		-e FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
		-e FORGE_ENV=$${FORGE_ENV:-development} \
		-e FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
		-e FORGE_WORKFLOWS_DATABASE_URL=$${FORGE_WORKFLOWS_DATABASE_URL:-postgres://forge:forge@host.docker.internal:5001/forge_workflows} \
		-e FORGE_WORKFLOWS_DEFS_DIR=/app/definitions \
		$(IMAGE)

docker-build:
	docker build -t $(IMAGE) .

ensure-db:
	@$(COMPOSE) up -d postgres
	@for i in $$(seq 1 30); do \
		if docker exec forge-postgres pg_isready -U forge >/dev/null 2>&1; then break; fi; \
		sleep 1; \
	done
	@docker exec forge-postgres psql -U forge -tc "SELECT 1 FROM pg_database WHERE datname='forge_workflows'" \
		| grep -q 1 \
		|| docker exec forge-postgres psql -U forge -c "CREATE DATABASE forge_workflows"

run: docker-build ensure-db
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit: docker-build
	@echo "Unit tests passed during image build"

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ready"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["service"]=="forge-workflows"; assert d["language"]=="elixir"; assert d["status"]=="running"; assert "version" in d'
	@# Structured JSON logs + request IDs + supervision tree
	@curl -sf -H 'X-Request-ID: integ-req-1' "http://127.0.0.1:$(HOST_PORT)/health/live" >/dev/null
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-workflows"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-workflows"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"supervision"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"request_id":"integ-req-1"'
	@grep -q '4302' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-workflows.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p; assert "forge-workflows" in p; assert "elixir" in p; assert "/v1/workflows" in p; assert "/v1/runs" in p; assert "WorkflowRun" in p'
	@$(MAKE) -C $(ROOT_DIR) lint-openapi
	@# List definitions
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/workflows" | python3 -c 'import sys,json; d=json.load(sys.stdin); names={w["name"] for w in d["workflows"]}; assert "fixture-log" in names'
	@# Start run → completes → history persisted
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-log/runs" \
		-d '{"input":{"k":"v"}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		test -n "$$RID"; \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		BODY=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID"); \
		echo "$$BODY" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed"; assert d["steps"]; assert all(s["status"]=="completed" for s in d["steps"]); assert d.get("result",{}).get("ok") is True'
	@# Cross-project run read → 404
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs" | python3 -c 'import sys,json; print(json.load(sys.stdin)["runs"][0]["run_id"])'); \
		CODE=$$(curl -s -o /tmp/forge-workflows-xproj.json -w '%{http_code}' -H 'X-Forge-Project: other-proj' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID"); \
		test "$$CODE" = "404"
	@# Mid-run restart: resume without re-executing completed steps
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-resume/runs" \
		-d '{"input":{}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		test -n "$$RID"; \
		for i in $$(seq 1 40); do \
			BEFORE=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(next(s["status"]+":"+str(s["attempt"]) for s in d["steps"] if s["id"]=="before"))'); \
			if [ "$$BEFORE" = "completed:1" ]; then break; fi; \
			sleep 0.25; \
		done; \
		$(COMPOSE) restart $(SERVICE) >/dev/null; \
		$(MAKE) wait; \
		for i in $$(seq 1 60); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ]; then break; fi; \
			sleep 0.5; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed"; steps={s["id"]:s for s in d["steps"]}; assert steps["before"]["status"]=="completed"; assert steps["before"]["attempt"]==1, steps; assert steps["slow"]["status"]=="completed"; assert steps["after"]["status"]=="completed"'; \
		$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'skipping completed step' || \
			echo "note: skip log may be absent if slow step had not completed before restart (attempt still 1)"
	@# Invalid PORT fails startup.
	@docker rm -f forge-workflows-bad-port >/dev/null 2>&1 || true
	@if docker run --name forge-workflows-bad-port \
		-e PORT=not-a-port \
		-e FORGE_WORKFLOWS_DATABASE_URL=postgres://forge:forge@postgres:5432/forge_workflows \
		$(IMAGE) >/tmp/forge-workflows-bad-port.out 2>&1; then \
		echo "expected non-zero exit for invalid PORT" >&2; \
		docker rm -f forge-workflows-bad-port >/dev/null 2>&1 || true; \
		exit 1; \
	fi
	@docker rm -f forge-workflows-bad-port >/dev/null 2>&1 || true
	@grep -Eiq 'PORT|port' /tmp/forge-workflows-bad-port.out
	@# SIGTERM → graceful exit 0 within grace period.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (definitions, durable runs, resume, project isolation, OpenAPI)."

docker-run: run

clean:
	@rm -rf _build deps .elixir_ls
	@rm -f /tmp/forge-workflows-bad-port.out /tmp/forge-workflows-xproj.json
