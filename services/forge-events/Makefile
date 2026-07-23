SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-events
IMAGE := forge/forge-events:local
HOST_PORT ?= 4105

.PHONY: dev build run test test-unit test-integration lint format docker-build docker-run clean wait

dev:
	PORT=$${PORT:-4105} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-events} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_NATS_URL=$${FORGE_NATS_URL:-nats://127.0.0.1:5002} \
	FORGE_EVENTS_STREAMS=$${FORGE_EVENTS_STREAMS:-build,deployment,runtime,application,agent} \
	FORGE_EVENT_MAX_BYTES=$${FORGE_EVENT_MAX_BYTES:-262144} \
	FORGE_CONSUME_MAX_BATCH=$${FORGE_CONSUME_MAX_BATCH:-100} \
	FORGE_CONSUME_WAIT_MS=$${FORGE_CONSUME_WAIT_MS:-2000} \
	FORGE_DEFAULT_ACK_WAIT_S=$${FORGE_DEFAULT_ACK_WAIT_S:-30} \
	FORGE_DEFAULT_MAX_DELIVERIES=$${FORGE_DEFAULT_MAX_DELIVERIES:-5} \
	FORGE_ACK_TOKEN_TTL_S=$${FORGE_ACK_TOKEN_TTL_S:-60} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	go run ./cmd/forge-events

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-events ./cmd/forge-events

run: docker-build
	@$(COMPOSE) up -d nats
	@$(COMPOSE) up -d --build $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 90
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 90

test: test-unit test-integration

test-unit:
	FORGE_NATS_URL=$${FORGE_NATS_URL:-nats://127.0.0.1:5002} go test ./...

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/" | python3 -c 'import sys,json; b=json.load(sys.stdin); assert b["service"]=="forge-events" and b["language"]=="go" and b["status"]=="running"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"service":"forge-events"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"message":"starting forge-events"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q 'events.bootstrap'
	@# Platform streams present via JetStream API (Go one-shot against compose NATS).
	@FORGE_EVENTS_EXPECT_STREAMS=1 FORGE_NATS_URL=nats://127.0.0.1:5002 go test ./internal/nats -run TestPlatformStreamsPresent -count=1
	@# Durable create → publish → consume → ack; ack_wait redelivery; unknown subject 400.
	@subj="application.crashed.make_$$$$"; cons="make_$$$$"; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/consumers" \
			-H 'content-type: application/json' \
			-d "{\"name\":\"$$cons\",\"subject\":\"$$subj\",\"ack_wait_s\":2,\"max_deliveries\":3}" \
			| python3 -c 'import sys,json; b=json.load(sys.stdin); assert b["name"] and b["ack_wait_s"]==2'; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/events" \
			-H 'content-type: application/json' \
			-d "{\"subject\":\"$$subj\",\"data\":{\"service\":\"demo\",\"reason\":\"oom\"},\"source\":\"runtime\"}" \
			| python3 -c 'import sys,json; b=json.load(sys.stdin); assert b.get("event_id") and b.get("stream")=="application" and int(b.get("seq",0))>0'; \
		tok="$$(curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/consume" \
			-H 'content-type: application/json' \
			-d "{\"consumer\":\"$$cons\",\"batch\":10}" \
			| python3 -c 'import sys,json; b=json.load(sys.stdin); assert len(b["messages"])>=1; m=b["messages"][0]; assert m["subject"].startswith("application.crashed"); assert m["data"]["service"]=="demo"; assert m.get("ack_token"); assert int(m.get("delivery_count",0))==1; print(m["ack_token"])')"; \
		code="$$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$(HOST_PORT)/v1/ack" \
			-H 'content-type: application/json' -d "{\"ack_token\":\"$$tok\"}")"; \
		test "$$code" = "204"; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/events" \
			-H 'content-type: application/json' \
			-d "{\"subject\":\"$$subj\",\"data\":{\"service\":\"demo\",\"reason\":\"retry\"},\"source\":\"runtime\"}" >/dev/null; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/consume" \
			-H 'content-type: application/json' \
			-d "{\"consumer\":\"$$cons\",\"batch\":1}" >/tmp/forge-events-noack.json; \
		python3 -c 'import json; m=json.load(open("/tmp/forge-events-noack.json"))["messages"][0]; assert int(m["delivery_count"])==1'; \
		sleep 3; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/consume" \
			-H 'content-type: application/json' \
			-d "{\"consumer\":\"$$cons\",\"batch\":1}" \
			| python3 -c 'import sys,json; b=json.load(sys.stdin); assert len(b["messages"])>=1; assert int(b["messages"][0]["delivery_count"])>=2'; \
		code="$$(curl -s -o /tmp/forge-events-bad-subject.json -w '%{http_code}' -X POST "http://127.0.0.1:$(HOST_PORT)/v1/events" -H 'content-type: application/json' -d '{"subject":"nope.bad","data":{}}')"; \
		test "$$code" = "400"
	@# Bad NATS URL → readiness 503 while live stays 200.
	@docker run --rm -d --name forge-events-bad-nats-$$$$ \
		-e PORT=8080 \
		-e FORGE_SERVICE_NAME=forge-events \
		-e FORGE_NATS_URL=nats://127.0.0.1:1 \
		-p 14105:8080 \
		$(IMAGE) >/tmp/forge-events-bad-nats.cid
	@cid="$$(cat /tmp/forge-events-bad-nats.cid)"; \
		trap 'docker rm -f "$$cid" >/dev/null 2>&1 || true' EXIT; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
			if curl -sf "http://127.0.0.1:14105/health/live" | grep -q '"status":"ok"'; then break; fi; \
			sleep 1; \
		done; \
		curl -sf "http://127.0.0.1:14105/health/live" | grep -q '"status":"ok"'; \
		code="$$(curl -s -o /tmp/forge-events-ready-503.json -w '%{http_code}' "http://127.0.0.1:14105/health/ready")"; \
		test "$$code" = "503"; \
		grep -q '"status":"not_ready"' /tmp/forge-events-ready-503.json; \
		docker rm -f "$$cid" >/dev/null
	@# NATS stop → readiness 503; start → recovers to 200 (live stays 200).
	@docker stop forge-nats >/dev/null
	@for i in $$(seq 1 45); do \
		code="$$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/health/ready" || true)"; \
		if [ "$$code" = "503" ]; then break; fi; \
		sleep 1; \
	done; \
	curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'; \
	code="$$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(HOST_PORT)/health/ready" || true)"; \
	test "$$code" = "503"; \
	docker start forge-nats >/dev/null; \
	for i in $$(seq 1 90); do \
		if curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'; then break; fi; \
		sleep 1; \
	done; \
	curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@# Restart → idempotent bootstrap; streams still present; ready again.
	@$(COMPOSE) restart $(SERVICE) >/dev/null
	@$(MAKE) wait
	@FORGE_EVENTS_EXPECT_STREAMS=1 FORGE_NATS_URL=nats://127.0.0.1:5002 go test ./internal/nats -run TestPlatformStreamsPresent -count=1
	@# Invalid PORT fails startup (config validation).
	@! PORT=not-a-port FORGE_NATS_URL=nats://127.0.0.1:5002 go run ./cmd/forge-events >/tmp/forge-events-bad-port.out 2>&1
	@grep -q 'PORT' /tmp/forge-events-bad-port.out
	@# SIGTERM / docker stop exits 0 within grace.
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@grep -q '4105' $(ROOT_DIR)/docs/operations/ports.md && echo port-reserved
	@echo "Integration checks passed (health + identity + JetStream streams + durable consume/ack/redelivery + unknown subject 400 + ready/503 + restart idempotency + invalid PORT + graceful SIGTERM)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-events-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .

docker-run: run

clean:
	rm -rf bin
	@rm -f /tmp/forge-events-bad-port.out /tmp/forge-events-gofmt.out \
		/tmp/forge-events-bad-nats.cid /tmp/forge-events-ready-503.json
