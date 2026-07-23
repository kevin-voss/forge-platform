SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-models
IMAGE := forge/forge-models:local
HOST_PORT ?= 4300

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait sync

sync:
	@command -v uv >/dev/null || (echo "uv is required (https://github.com/astral-sh/uv)" >&2; exit 1)
	uv sync --extra dev

dev: sync
	PORT=$${PORT:-4300} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-models} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_MODELS_BACKEND=$${FORGE_MODELS_BACKEND:-fake} \
	FORGE_MODELS_CONFIG=$${FORGE_MODELS_CONFIG:-} \
	FORGE_MODELS_EMBED_MAX_BATCH=$${FORGE_MODELS_EMBED_MAX_BATCH:-64} \
	FORGE_MODELS_EMBED_MAX_CHARS=$${FORGE_MODELS_EMBED_MAX_CHARS:-8192} \
	FORGE_MODELS_GEN_MAX_TOKENS=$${FORGE_MODELS_GEN_MAX_TOKENS:-512} \
	FORGE_MODELS_GEN_DEFAULT_TEMP=$${FORGE_MODELS_GEN_DEFAULT_TEMP:-0} \
	FORGE_MODELS_CLASSIFY_MAX_LABELS=$${FORGE_MODELS_CLASSIFY_MAX_LABELS:-32} \
	FORGE_MODELS_STREAM_TIMEOUT_SECONDS=$${FORGE_MODELS_STREAM_TIMEOUT_SECONDS:-60} \
	FORGE_MODELS_JOB_TTL_SECONDS=$${FORGE_MODELS_JOB_TTL_SECONDS:-3600} \
	FORGE_MODELS_MAX_CONCURRENT_JOBS=$${FORGE_MODELS_MAX_CONCURRENT_JOBS:-4} \
	FORGE_MODELS_JOB_TIMEOUT_SECONDS=$${FORGE_MODELS_JOB_TIMEOUT_SECONDS:-300} \
	FORGE_MODELS_LOCAL_MODEL_PATH=$${FORGE_MODELS_LOCAL_MODEL_PATH:-} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	uv run python -m app.main

build: sync
	uv sync --extra dev

run: docker-build
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit: sync
	PORT=4300 FORGE_MODELS_BACKEND=fake FORGE_LOG_LEVEL=error uv run pytest -q

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"live"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ready"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["service"]=="forge-models"; assert d["language"]=="python"; assert d["status"]=="running"; assert "version" in d'
	@# Structured JSON logs + request IDs
	@curl -sf -H 'X-Request-ID: integ-req-1' "http://127.0.0.1:$(HOST_PORT)/health/live" >/dev/null
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-models"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-models"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"request_id":"integ-req-1"'
	@grep -q '4300' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@python3 -c 'p=open("$(ROOT_DIR)/contracts/openapi/forge-models.openapi.yaml").read(); assert "/health/live" in p and "/health/ready" in p and "\n  /:" in p; assert "forge-models" in p; assert "/v1/models" in p; assert "/v1/models/{model}/embed" in p; assert "EmbedResponse" in p; assert "/v1/models/{model}/generate" in p; assert "/v1/models/{model}/classify" in p; assert "/v1/models/{model}/summarize" in p; assert "TokenUsage" in p; assert "/v1/jobs" in p; assert "text/event-stream" in p; assert "CreateJobResponse" in p'
	@# Registry + deterministic embed smoke (manual verification from 14.03).
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/v1/models" | python3 -c 'import sys,json; d=json.load(sys.stdin); ids={m["id"] for m in d["models"]}; assert "local-embed-small" in ids'
	@curl -sf -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/models/local-embed-small/embed" \
		-H 'content-type: application/json' -d '{"input":"hello forge"}' \
		| python3 -c 'import sys,json; d=json.load(sys.stdin); assert len(d["embeddings"][0])==d["dim"]==384'
	@a=$$(curl -sf -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/models/local-embed-small/embed" -H 'content-type: application/json' -d '{"input":"x"}'); \
		b=$$(curl -sf -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/models/local-embed-small/embed" -H 'content-type: application/json' -d '{"input":"x"}'); \
		test "$$a" = "$$b" && echo "deterministic OK"
	@curl -sf -o /dev/null -w '%{http_code}' -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/models/local-general/embed" \
		-H 'content-type: application/json' -d '{"input":"x"}' | grep -q '^422$$'
	@# Generate/classify/summarize smoke (manual verification from 14.04).
	@BASE=http://127.0.0.1:$(HOST_PORT); M=local-general; \
		curl -fsS -XPOST $$BASE/v1/models/$$M/generate -H 'content-type: application/json' \
		  -d '{"prompt":"summarize: forge platform","max_tokens":32,"temperature":0}' | grep -q '"text"'; \
		curl -fsS -XPOST $$BASE/v1/models/$$M/classify -H 'content-type: application/json' \
		  -d '{"input":"database connection refused","labels":["network","auth","disk"]}' | grep -q '"labels"'; \
		curl -fsS -XPOST $$BASE/v1/models/$$M/summarize -H 'content-type: application/json' \
		  -d '{"input":"long incident text ..."}' | grep -q '"summary"'
	@curl -sf -o /dev/null -w '%{http_code}' -XPOST "http://127.0.0.1:$(HOST_PORT)/v1/models/local-embed-small/generate" \
		-H 'content-type: application/json' -d '{"prompt":"x","temperature":0}' | grep -q '^422$$'
	@# Streaming + async jobs smoke (manual verification from 14.05).
	@BASE=http://127.0.0.1:$(HOST_PORT); M=local-general; \
		curl -fsS -N -XPOST "$$BASE/v1/models/$$M/generate?stream=true" \
		  -H 'content-type: application/json' -d '{"prompt":"stream please","max_tokens":16}' \
		  | head -n 20 | grep -q 'data:'; \
		JID=$$(curl -fsS -XPOST $$BASE/v1/jobs -H 'content-type: application/json' \
		  -H 'X-Forge-Project: integ' \
		  -d '{"model":"'$$M'","task":"summarize","input":"async integ summary"}' \
		  | python3 -c 'import sys,json; print(json.load(sys.stdin)["job_id"])'); \
		test -n "$$JID"; \
		for i in 1 2 3 4 5 6 7 8 9 10; do \
		  STATUS=$$(curl -fsS $$BASE/v1/jobs/$$JID -H 'X-Forge-Project: integ' \
		    | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'); \
		  case "$$STATUS" in succeeded|failed|cancelled) break ;; esac; \
		  sleep 0.2; \
		done; \
		echo "$$STATUS" | grep -Eq 'succeeded|failed|cancelled'
	@# Unknown backend fails process startup (ephemeral container).
	@docker rm -f forge-models-bad-backend >/dev/null 2>&1 || true
	@if docker run --name forge-models-bad-backend \
		-e PORT=8080 \
		-e FORGE_MODELS_BACKEND=openai \
		$(IMAGE) >/tmp/forge-models-bad-backend.out 2>&1; then \
		echo "expected non-zero exit for unknown backend" >&2; \
		docker rm -f forge-models-bad-backend >/dev/null 2>&1 || true; \
		exit 1; \
	fi
	@docker rm -f forge-models-bad-backend >/dev/null 2>&1 || true
	@grep -Eiq 'forge_models_backend|FORGE_MODELS_BACKEND|validation error|Input should be' /tmp/forge-models-bad-backend.out
	@# Invalid PORT fails startup.
	@docker rm -f forge-models-bad-port >/dev/null 2>&1 || true
	@if docker run --name forge-models-bad-port \
		-e PORT=not-a-port \
		-e FORGE_MODELS_BACKEND=fake \
		$(IMAGE) >/tmp/forge-models-bad-port.out 2>&1; then \
		echo "expected non-zero exit for invalid PORT" >&2; \
		docker rm -f forge-models-bad-port >/dev/null 2>&1 || true; \
		exit 1; \
	fi
	@docker rm -f forge-models-bad-port >/dev/null 2>&1 || true
	@grep -Eiq 'PORT|port|validation error' /tmp/forge-models-bad-port.out
	@# SIGTERM → graceful exit 0 within grace period.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health, identity, registry, embed/gen/classify/summarize, stream/jobs, JSON logs/request IDs, OpenAPI, bad backend/PORT, SIGTERM)."

lint: sync
	uv run ruff check app tests
	uv run ruff format --check app tests

format: sync
	uv run ruff format app tests

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	@rm -rf .venv .pytest_cache .ruff_cache __pycache__ app/__pycache__ tests/__pycache__
	@rm -f /tmp/forge-models-bad-backend.out /tmp/forge-models-bad-port.out
	@find . -type d -name '__pycache__' -prune -exec rm -rf {} +
