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
	contract-validate \
	lint format clean reset demo service-test service-run wait

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
	@echo "  make format                Format repository files where applicable"
	@echo "  make clean                 Remove local build artifacts"
	@echo "  make reset                 Destroy local volumes and restart clean"
	@echo "  make demo DEMO=00          Run a numbered demo (e.g. DEMO=12 observability)"
	@echo "  make service-test SERVICE= Run tests for one service"
	@echo "  make service-run SERVICE=  Run one service locally"

setup: env-check
	@chmod +x scripts/*.sh scripts/lib/*.sh demos/*/run.sh \
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

lint:
	@echo "Checking shell scripts with bash -n..."
	@find scripts demos tests tools -name '*.sh' -print0 | xargs -0 -n1 bash -n
	@python3 -m py_compile tools/contract-validator/validate.py tools/contract-validator/fixture_server.py
	@echo "Lint complete."

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
	@demo_num="$$(printf '%02d' $$((10#$(DEMO))))"; \
	matches=(demos/$${demo_num}-*/run.sh); \
	if [[ ! -f "$${matches[0]}" ]]; then \
		echo "Demo not found for DEMO=$(DEMO)" >&2; \
		exit 1; \
	fi; \
	echo "Running $${matches[0]}"; \
	exec -a "forge-demo-$${demo_num}" bash "$${matches[0]}"

service-test:
	@if [[ -z "$(SERVICE)" ]]; then echo "SERVICE is required" >&2; exit 1; fi
	@if [[ ! -d "services/$(SERVICE)" ]]; then echo "Unknown service: $(SERVICE)" >&2; exit 1; fi
	@$(MAKE) -C "services/$(SERVICE)" test

service-run:
	@if [[ -z "$(SERVICE)" ]]; then echo "SERVICE is required" >&2; exit 1; fi
	@if [[ ! -d "services/$(SERVICE)" ]]; then echo "Unknown service: $(SERVICE)" >&2; exit 1; fi
	@$(MAKE) -C "services/$(SERVICE)" run
