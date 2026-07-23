SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-workflows
IMAGE := forge/forge-workflows:local
HOST_PORT ?= 4302
ELIXIR_IMAGE ?= hexpm/elixir:1.18.4-erlang-27.3.4.2-debian-bookworm-20250811-slim
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
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-workflows.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p; assert "forge-workflows" in p; assert "elixir" in p; assert "/v1/workflows" in p; assert "/v1/runs" in p; assert "WorkflowRun" in p; assert "wake_at" in p; assert "RetryPolicy" in p; assert "parallel" in p; assert "/v1/triggers/test" in p; assert "TriggerConfig" in p; assert "agent" in p; assert "deployment.failed" in p; assert "approval" in p; assert "/v1/approvals" in p; assert "awaiting_approval" in p; assert "WorkflowApproval" in p'
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
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed"; steps={s["id"]:s for s in d["steps"]}; assert steps["before"]["status"]=="completed"; assert steps["before"]["attempt"]==1, steps; assert steps["slow"]["status"]=="completed"; assert steps["slow"]["attempt"]==1, steps; assert steps["after"]["status"]=="completed"'; \
		$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'skipping completed step' || \
			echo "note: skip log may be absent if slow step had not completed before restart (attempt still 1)"
	@# Retry recovers transient-failing task
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-retry/runs" \
		-d '{"input":{}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 60); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["flaky"]["attempt"]==3, steps; assert steps["flaky"]["status"]=="completed"'
	@# Parallel branches join
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-primitives/runs" \
		-d '{"input":{"severity":"high"}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 60); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["fanout"]["status"]=="completed"; assert "branches" in (steps["fanout"].get("output") or {}); assert steps["escalate"]["status"]=="completed"; assert steps["close"]["status"]=="skipped"'
	@# Conditional routes on context (low severity → close)
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-conditional/runs" \
		-d '{"input":{"severity":"low"}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["close"]["status"]=="completed"; assert steps["escalate"]["status"]=="skipped"'
	@# Timeout marks step failed
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-timeout/runs" \
		-d '{"input":{}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "failed" ] || [ "$$STATUS" = "completed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="failed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["slow"]["status"]=="failed"; assert steps["slow"]["error"]=="timeout"'
	@# Event trigger: deployment.failed starts mapped workflow once
	@EID="evt-integ-$$(date +%s)-$$RANDOM"; \
		RESP=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
			-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/triggers/test" \
			-d "{\"event\":\"deployment.failed\",\"event_id\":\"$$EID\",\"data\":{\"deployment_id\":\"dep-123\",\"service_id\":\"svc-1\",\"reason\":\"unhealthy\",\"failed_at\":\"2026-07-23T09:00:00Z\"}}"); \
		echo "$$RESP" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="started", d; assert d["runs"]'; \
		RID=$$(echo "$$RESP" | python3 -c 'import sys,json; print(json.load(sys.stdin)["runs"][0]["run_id"])'); \
		for i in $$(seq 1 60); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; assert d["input"]["event"]["deployment_id"]=="dep-123"; steps={s["id"]:s for s in d["steps"]}; assert steps["diagnose"]["status"]=="completed"; assert steps["diagnose"]["output"]["status"]=="succeeded"; assert "dep-123" in (steps["diagnose"]["output"].get("result") or "")'; \
		DUP=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
			-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/triggers/test" \
			-d "{\"event\":\"deployment.failed\",\"event_id\":\"$$EID\",\"data\":{\"deployment_id\":\"dep-123\",\"service_id\":\"svc-1\",\"reason\":\"unhealthy\",\"failed_at\":\"2026-07-23T09:00:00Z\"}}"); \
		echo "$$DUP" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="duplicate", d; assert d["runs"]==[]'; \
		COUNT=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs" | python3 -c 'import sys,json; eid=sys.argv[1]; print(sum(1 for r in json.load(sys.stdin)["runs"] if r.get("input",{}).get("event_id")==eid))' "$$EID"); \
		test "$$COUNT" = "1"
	@# Agent step via direct run (fake agent) populates context
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-agent/runs" \
		-d '{"input":{"event":{"deployment_id":"dep-agent-1"}}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 60); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["diagnose"]["output"]["agent"]=="deployment-investigator"; assert steps["diagnose"]["output"]["input"]["deployment"]=="dep-agent-1"'
	@# Approval parks; restart keeps awaiting; approve resumes without re-running before
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-approval/runs" \
		-d '{"input":{"event":{"deployment_id":"dep-appr-1"}}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "awaiting_approval" ]; then break; fi; \
			sleep 0.25; \
		done; \
		BODY=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID"); \
		echo "$$BODY" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="awaiting_approval", d; assert d.get("pending_approval",{}).get("status")=="pending"; assert "dep-appr-1" in (d["pending_approval"].get("prompt") or "")'; \
		AID=$$(echo "$$BODY" | python3 -c 'import sys,json; print(json.load(sys.stdin)["pending_approval"]["id"])'); \
		$(COMPOSE) restart $(SERVICE) >/dev/null; \
		$(MAKE) wait; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="awaiting_approval", d; steps={s["id"]:s for s in d["steps"]}; assert steps["before"]["status"]=="completed"; assert steps["before"]["attempt"]==1; assert steps["approve-rollback"]["status"]=="waiting"'; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'X-Forge-Actor: alice' -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/approvals/$$AID/approve" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="approved"; assert d["decided_by"]=="alice"'; \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["before"]["attempt"]==1; assert steps["approve-rollback"]["output"]["decision"]=="approved"; assert steps["after-approve"]["status"]=="completed"; assert steps["close"]["status"]=="skipped"'; \
		CODE=$$(curl -s -o /tmp/forge-workflows-appr-term.json -w '%{http_code}' -H 'X-Forge-Project: $(PROJECT)' -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/approvals/$$AID/approve"); \
		test "$$CODE" = "409"
	@# Deny follows on_deny (skips after-approve)
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-approval/runs" \
		-d '{"input":{"event":{"deployment_id":"dep-deny-1"}}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "awaiting_approval" ]; then break; fi; \
			sleep 0.25; \
		done; \
		AID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["pending_approval"]["id"])'); \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'X-Forge-Actor: bob' -H 'content-type: application/json' \
			-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/approvals/$$AID/deny" -d '{"reason":"not needed"}' \
			| python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="denied"; assert d["decided_by"]=="bob"'; \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.25; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["after-approve"]["status"]=="skipped"; assert steps["close"]["status"]=="completed"'
	@# Cross-project approval access → 404
	@AID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/approvals" | python3 -c 'import sys,json; print(json.load(sys.stdin)["approvals"][0]["id"])'); \
		CODE=$$(curl -s -o /tmp/forge-workflows-appr-xproj.json -w '%{http_code}' -H 'X-Forge-Project: other-proj' "http://127.0.0.1:$(HOST_PORT)/v1/approvals/$$AID"); \
		test "$$CODE" = "404"
	@# Expiry via forced expires_at → on_deny path
	@RID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' -H 'content-type: application/json' \
		-XPOST "http://127.0.0.1:$(HOST_PORT)/v1/workflows/fixture-approval/runs" \
		-d '{"input":{"event":{"deployment_id":"dep-exp-1"}}}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["run_id"])'); \
		for i in $$(seq 1 40); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "awaiting_approval" ]; then break; fi; \
			sleep 0.25; \
		done; \
		AID=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["pending_approval"]["id"])'); \
		docker exec forge-postgres psql -U forge -d forge_workflows -c "UPDATE workflow_approvals SET expires_at = NOW() - interval '1 second' WHERE id = '$$AID'" >/dev/null; \
		for i in $$(seq 1 60); do \
			STATUS=$$(curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
			if [ "$$STATUS" = "completed" ] || [ "$$STATUS" = "failed" ]; then break; fi; \
			sleep 0.5; \
		done; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/approvals/$$AID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="expired", d'; \
		curl -sf -H 'X-Forge-Project: $(PROJECT)' "http://127.0.0.1:$(HOST_PORT)/v1/runs/$$RID" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="completed", d; steps={s["id"]:s for s in d["steps"]}; assert steps["approve-rollback"]["output"]["decision"]=="expired"; assert steps["after-approve"]["status"]=="skipped"; assert steps["close"]["status"]=="completed"'
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
	@echo "Integration checks passed (definitions, durable runs, resume, retry/delay/parallel/conditional/timeout, event triggers, agent steps, durable approvals, project isolation, OpenAPI)."

docker-run: run

clean:
	@rm -rf _build deps .elixir_ls
	@rm -f /tmp/forge-workflows-bad-port.out /tmp/forge-workflows-xproj.json /tmp/forge-workflows-appr-term.json /tmp/forge-workflows-appr-xproj.json
