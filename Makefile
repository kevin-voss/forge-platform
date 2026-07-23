SHELL := /bin/bash
.DEFAULT_GOAL := help

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
COMPOSE := docker compose
DEMO ?= 00
SERVICE ?=

BASE_URL ?=
EXPECT_SERVICE ?=
EXPECT_LANGUAGE ?=
LOG_FILE ?=
SHUTDOWN_PID ?=
SHUTDOWN_CONTAINER ?=
SHUTDOWN_TIMEOUT ?= 10s

.PHONY: help setup env-check infra-up infra-down dev stop restart status logs \
	build build-cli test test-unit test-integration test-e2e test-infrastructure \
	contract-validate lint-openapi \
	lint format clean reset demo demo-accept demo-full service-test service-run wait \
	e2e-install test-platform-e2e e2e-report

HEADLESS ?=
PROJECTS ?=
KEEP ?=
FINDINGS_ONLY ?=

help:
	@echo "Forge Platform Make targets"
	@echo "  make setup                 Install local tooling expectations and create .env"
	@echo "  make infra-up              Start foundational infrastructure only"
	@echo "  make infra-down            Stop foundational infrastructure"
	@echo "  make dev                   Start local platform infrastructure"
	@echo "  make stop                  Stop Compose services"
	@echo "  make restart               Restart Compose services"
	@echo "  make status                Show Compose and health status"
	@echo "  make logs                  Tail Compose logs"
	@echo "  make build                 Build workspace artifacts (noop in step 00)"
	@echo "  make build-cli             Build the forge CLI binary (tools/forge-cli)"
	@echo "  make test                  Run all available test suites"
	@echo "  make test-unit             Run unit/contract-validator tests"
	@echo "  make test-infrastructure   Verify local infrastructure health"
	@echo "  make contract-validate     Validate a running workload (BASE_URL=...)"
	@echo "  make lint                  Run repository lint checks"
	@echo "  make lint-openapi          Validate contracts/openapi/*.openapi.yaml"
	@echo "  make format                Format repository files where applicable"
	@echo "  make clean                 Remove local build artifacts"
	@echo "  make reset                 Destroy local volumes and restart clean"
	@echo "  make demo DEMO=00          Run a numbered demo (e.g. DEMO=12 observability)"
	@echo "  make demo DEMO=20          Declarative resource API gate (epic 20)"
	@echo "  make demo DEMO=21          Service discovery gate (epic 21)"
	@echo "  make demo DEMO=22          Forge network gate (epic 22)"
	@echo "  make demo DEMO=23          Local cloud infrastructure gate (epic 23)"
	@echo "  make demo DEMO=24          Autoscaling gate (epic 24)"
	@echo "  make demo DEMO=25          HA placement M1 exit gate (epic 25)"
	@echo "  make demo DEMO=09-full-platform  Start capstone (start.sh)"
	@echo "  make demo-accept DEMO=...  Run demo acceptance suite (capstone accept.sh)"
	@echo "  make demo-full             Alias: demo DEMO=09-full-platform"
	@echo "  make service-test SERVICE= Run tests for one service"
	@echo "  make service-run SERVICE=  Run one service locally"
	@echo "  make e2e-install           Install Playwright browsers for tests/e2e"
	@echo "  make test-platform-e2e     Run platform E2E orchestrator (HEADLESS/PROJECTS/KEEP)"
	@echo "  make e2e-report            Open the last platform E2E HTML report"
	@echo "  make demo DEMO=5X          Demo product (demo.json → orchestrator lifecycle)"

setup: env-check
	@chmod +x scripts/*.sh scripts/lib/*.sh demos/*/run.sh \
		demos/21-service-discovery/run.sh \
		demos/22-forge-network/run.sh demos/22-forge-network/lib/verify.sh \
		demos/24-autoscaling/run.sh \
		demos/25-ha-placement/run.sh \
		demos/09-full-platform/start.sh demos/09-full-platform/accept.sh \
		demos/09-full-platform/tests/*.sh \
		demos/09-full-platform/ai/*.sh demos/09-full-platform/scenario/*.sh \
		tests/infrastructure/test_infrastructure.sh \
		tests/contracts/test_runtime_contract_validator.sh \
		tools/contract-validator/*.sh tools/contract-validator/*.py
	@command -v docker >/dev/null || (echo "docker is required" >&2; exit 1)
	@command -v curl >/dev/null || (echo "curl is required" >&2; exit 1)
	@command -v python3 >/dev/null || (echo "python3 is required" >&2; exit 1)
	@docker compose version >/dev/null
	@echo "Setup complete."

env-check:
	@if [[ ! -f .env ]]; then \
		cp .env.example .env; \
		echo "Created .env from .env.example"; \
	fi

infra-up: env-check
	$(COMPOSE) up -d

infra-down:
	$(COMPOSE) down --remove-orphans

dev: infra-up wait
	@echo "Local Forge infrastructure is up."

stop: infra-down

restart: stop dev

status:
	@$(COMPOSE) ps
	@echo
	@./scripts/smoke-test.sh || true

logs:
	$(COMPOSE) logs -f --tail=200

wait:
	@./scripts/wait-for-service.sh http://127.0.0.1:5003/healthz 90
	@./scripts/wait-for-service.sh http://127.0.0.1:5000/v2/ 90
	@./scripts/wait-for-service.sh http://127.0.0.1:13133/ 90
	@./scripts/wait-for-service.sh http://127.0.0.1:3001/-/healthy 90
	@./scripts/wait-for-service.sh http://127.0.0.1:3002/ready 90
	@./scripts/wait-for-service.sh http://127.0.0.1:3003/ready 90
	@./scripts/wait-for-service.sh http://127.0.0.1:3000/api/health 90
	@echo "Waiting for PostgreSQL on 5001..."
	@for i in $$(seq 1 90); do \
		if (echo >/dev/tcp/127.0.0.1/5001) >/dev/null 2>&1; then \
			echo "Ready: PostgreSQL"; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "Timed out waiting for PostgreSQL" >&2; \
	exit 1

build:
	@echo "No service builds in Step 00."

build-cli:
	@$(MAKE) -C tools/forge-cli build

test: test-unit test-infrastructure
	@echo "All available tests passed."

test-unit:
	@./tools/contract-validator/test_validator.sh

test-integration: test-infrastructure

test-e2e:
	@echo "No end-to-end product tests in Step 00."

# Platform E2E harness (epic 50): install browsers + orchestrator entry point.
e2e-install:
	@cd tests/e2e && npm ci --no-audit --no-fund
	@cd tests/e2e && npx playwright install --with-deps

# Headed by default; HEADLESS=1 (or CI=1) for CI. PROJECTS=01,50 subsets; KEEP=1 skips teardown.
test-platform-e2e:
	@cd tests/e2e && npm ci --no-audit --no-fund
	@cd tests/e2e && npm run build
	@cd tests/e2e && HEADLESS="$(HEADLESS)" PROJECTS="$(PROJECTS)" KEEP="$(KEEP)" FINDINGS_ONLY="$(FINDINGS_ONLY)" \
		node harness/orchestrator.js

# Open artifacts/report.html from the last orchestrator run (written by report.ts).
e2e-report:
	@cd tests/e2e && npm run build
	@cd tests/e2e && node harness/report.js --open

test-infrastructure: env-check
	@./tests/infrastructure/test_infrastructure.sh

contract-validate:
	@if [[ -z "$(BASE_URL)" ]]; then \
		echo "BASE_URL is required, e.g. BASE_URL=http://127.0.0.1:4201" >&2; \
		exit 1; \
	fi
	@args=(--base-url "$(BASE_URL)" --shutdown-timeout "$(SHUTDOWN_TIMEOUT)"); \
	if [[ -n "$(EXPECT_SERVICE)" ]]; then args+=(--expect-service "$(EXPECT_SERVICE)"); fi; \
	if [[ -n "$(EXPECT_LANGUAGE)" ]]; then args+=(--expect-language "$(EXPECT_LANGUAGE)"); fi; \
	if [[ -n "$(LOG_FILE)" ]]; then args+=(--log-file "$(LOG_FILE)"); fi; \
	if [[ -n "$(SHUTDOWN_PID)" ]]; then args+=(--shutdown-pid "$(SHUTDOWN_PID)"); fi; \
	if [[ -n "$(SHUTDOWN_CONTAINER)" ]]; then args+=(--shutdown-container "$(SHUTDOWN_CONTAINER)"); fi; \
	./tools/contract-validator/run.sh "$${args[@]}"

lint: lint-openapi
	@echo "Checking shell scripts with bash -n..."
	@find scripts demos tests tools -name '*.sh' -print0 | xargs -0 -n1 bash -n
	@python3 -m py_compile tools/contract-validator/validate.py tools/contract-validator/fixture_server.py scripts/lint-openapi.py
	@echo "Lint complete."

lint-openapi:
	@python3 scripts/lint-openapi.py

format:
	@echo "No formatters configured in Step 00."

clean:
	@rm -rf .tmp .tmpdata
	@echo "Clean complete."

reset:
	@./scripts/reset-local.sh
	@$(MAKE) setup
	@$(MAKE) dev

demo:
	@if [[ -z "$(DEMO)" ]]; then echo "DEMO is required, e.g. DEMO=00" >&2; exit 1; fi
	@if [[ "$(DEMO)" == "09-full-platform" || "$(DEMO)" == "full-platform" || "$(DEMO)" == "full" ]]; then \
		echo "Starting demos/09-full-platform/start.sh"; \
		chmod +x demos/09-full-platform/start.sh; \
		exec -a "forge-demo-09-full-platform" bash demos/09-full-platform/start.sh; \
	fi
	@demo_num="$$(printf '%02d' $$((10#$(DEMO))))"; \
	demo_json=(demos/$${demo_num}-*/demo.json); \
	if [[ -f "$${demo_json[0]}" ]]; then \
		echo "Running platform E2E lifecycle for DEMO=$(DEMO) via $${demo_json[0]}"; \
		$(MAKE) test-platform-e2e PROJECTS="$${demo_num}" HEADLESS="$(HEADLESS)" KEEP="$(KEEP)" FINDINGS_ONLY="$(FINDINGS_ONLY)"; \
		exit $$?; \
	fi; \
	matches=(demos/$${demo_num}-*/run.sh); \
	if [[ ! -f "$${matches[0]}" ]]; then \
		echo "Demo not found for DEMO=$(DEMO)" >&2; \
		exit 1; \
	fi; \
	echo "Running $${matches[0]}"; \
	exec -a "forge-demo-$${demo_num}" bash "$${matches[0]}"

demo-accept:
	@if [[ -z "$(DEMO)" ]]; then echo "DEMO is required, e.g. DEMO=09-full-platform" >&2; exit 1; fi
	@if [[ "$(DEMO)" == "09-full-platform" || "$(DEMO)" == "full-platform" || "$(DEMO)" == "full" ]]; then \
		echo "Running demos/09-full-platform/accept.sh"; \
		chmod +x demos/09-full-platform/accept.sh demos/09-full-platform/tests/*.sh; \
		exec -a "forge-demo-accept-09-full-platform" bash demos/09-full-platform/accept.sh; \
	fi
	@demo_num="$$(printf '%02d' $$((10#$(DEMO))))"; \
	matches=(demos/$${demo_num}-*/accept.sh demos/$${demo_num}-*/acceptance.sh); \
	script=""; \
	for m in "$${matches[@]}"; do \
		if [[ -f "$$m" ]]; then script="$$m"; break; fi; \
	done; \
	if [[ -z "$$script" ]]; then \
		echo "Acceptance script not found for DEMO=$(DEMO) (tried accept.sh / acceptance.sh)" >&2; \
		exit 1; \
	fi; \
	echo "Running $${script}"; \
	exec bash "$${script}"

demo-full:
	@$(MAKE) demo DEMO=09-full-platform

service-test:
	@if [[ -z "$(SERVICE)" ]]; then echo "SERVICE is required" >&2; exit 1; fi
	@if [[ ! -d "services/$(SERVICE)" ]]; then echo "Unknown service: $(SERVICE)" >&2; exit 1; fi
	@$(MAKE) -C "services/$(SERVICE)" test

service-run:
	@if [[ -z "$(SERVICE)" ]]; then echo "SERVICE is required" >&2; exit 1; fi
	@if [[ ! -d "services/$(SERVICE)" ]]; then echo "Unknown service: $(SERVICE)" >&2; exit 1; fi
	@$(MAKE) -C "services/$(SERVICE)" run
