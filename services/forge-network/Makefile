SHELL := /bin/bash

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
COMPOSE := docker compose -f $(ROOT_DIR)/compose.yaml --project-directory $(ROOT_DIR)
SERVICE := forge-network
IMAGE := forge/forge-network:local
HOST_PORT ?= 4110

.PHONY: dev build run test test-unit test-integration lint format docker-build wait

dev:
	PORT=$${PORT:-8080} \
	FORGE_SERVICE_NAME=$${FORGE_SERVICE_NAME:-forge-network} \
	FORGE_SERVICE_VERSION=$${FORGE_SERVICE_VERSION:-0.1.0} \
	FORGE_LOG_LEVEL=$${FORGE_LOG_LEVEL:-info} \
	FORGE_ENV=$${FORGE_ENV:-development} \
	FORGE_SHUTDOWN_GRACE_SECONDS=$${FORGE_SHUTDOWN_GRACE_SECONDS:-10} \
	FORGE_DATABASE_URL=$${FORGE_DATABASE_URL:-postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable} \
	FORGE_DATABASE_SCHEMA=$${FORGE_DATABASE_SCHEMA:-network} \
	FORGE_DATABASE_POOL_MAX=$${FORGE_DATABASE_POOL_MAX:-10} \
	FORGE_DATABASE_MIGRATE_ON_START=$${FORGE_DATABASE_MIGRATE_ON_START:-true} \
	FORGE_NETWORK_CLUSTER_CIDR=$${FORGE_NETWORK_CLUSTER_CIDR:-10.100.0.0/16} \
	FORGE_NETWORK_NODE_PREFIX_LEN=$${FORGE_NETWORK_NODE_PREFIX_LEN:-24} \
	FORGE_NETWORK_IPV6_ENABLED=$${FORGE_NETWORK_IPV6_ENABLED:-false} \
	FORGE_NETWORK_LEASE_RECLAIM_INTERVAL_S=$${FORGE_NETWORK_LEASE_RECLAIM_INTERVAL_S:-60} \
	FORGE_NETWORK_DOCKER_COLLISION_CHECK=$${FORGE_NETWORK_DOCKER_COLLISION_CHECK:-true} \
	DOCKER_HOST=$${DOCKER_HOST:-unix:///var/run/docker.sock} \
	go run .

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/forge-network .

run: docker-build
	@$(COMPOSE) up -d --build postgres $(SERVICE)
	@$(MAKE) wait
	@echo "$(SERVICE) is up on http://127.0.0.1:$(HOST_PORT)"

wait:
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/live 120
	@$(ROOT_DIR)/scripts/wait-for-service.sh http://127.0.0.1:$(HOST_PORT)/health/ready 120

test: test-unit test-integration

test-unit:
	go test ./...

test-integration: run
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/live" | grep -q '"status":"ok"'
	@curl -sf "http://127.0.0.1:$(HOST_PORT)/health/ready" | grep -q '"status":"ok"'
	@$(COMPOSE) logs $(SERVICE) 2>&1 | grep -q '"event":"network.started"'
	@NET_NAME="overlay-itest-$$RANDOM"; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/networks" \
			-H 'content-type: application/json' \
			-d "{\"name\":\"$$NET_NAME\",\"spec\":{\"clusterCidr\":\"10.100.0.0/16\",\"nodePrefixLength\":24}}" \
			| tee /tmp/forge-network-create.json | grep -q '"phase":"Ready"'; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/networks/$$NET_NAME/node-leases" \
			-H 'content-type: application/json' -d '{"node_id":"node-a"}' \
			| tee /tmp/forge-network-node-a.json | grep -q '"cidr":"10.100.1.0/24"'; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/networks/$$NET_NAME/node-leases" \
			-H 'content-type: application/json' -d '{"node_id":"node-b"}' \
			| tee /tmp/forge-network-node-b.json | grep -q '"cidr":"10.100.2.0/24"'; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/networks/$$NET_NAME/workload-leases" \
			-H 'content-type: application/json' -d '{"node_id":"node-a","workload_id":"wl_1"}' \
			| tee /tmp/forge-network-wl.json | grep -Eq '"address":"10\.100\.1\.[0-9]+"'; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/networks/$$NET_NAME/node-leases" \
			-H 'content-type: application/json' -d '{"node_id":"node-a"}' \
			| grep -q '"cidr":"10.100.1.0/24"'; \
		code="$$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "http://127.0.0.1:$(HOST_PORT)/v1/networks/$$NET_NAME/node-leases/node-a")"; \
			test "$$code" = "204"; \
		curl -sf -X POST "http://127.0.0.1:$(HOST_PORT)/v1/networks/$$NET_NAME/node-leases" \
			-H 'content-type: application/json' -d '{"node_id":"node-a"}' \
			| tee /tmp/forge-network-node-a-re.json | grep -vq '"cidr":"10.100.2.0/24"'
	@# Collision: overlapping provider CIDR (compose sets 10.255.0.0/16) refused
	@code="$$(curl -s -o /tmp/forge-network-collide.json -w '%{http_code}' -X POST "http://127.0.0.1:$(HOST_PORT)/v1/networks" \
		-H 'content-type: application/json' \
		-d "{\"name\":\"collide-$$RANDOM\",\"spec\":{\"clusterCidr\":\"10.255.0.0/16\",\"nodePrefixLength\":24}}")"; \
		test "$$code" = "409"; \
		grep -q 'CidrCollision' /tmp/forge-network-collide.json
	@cid="$$($(COMPOSE) ps -q $(SERVICE))"; \
		test -n "$$cid"; \
		docker stop -t 15 "$$cid" >/dev/null; \
		code="$$(docker inspect -f '{{.State.ExitCode}}' "$$cid")"; \
		test "$$code" = "0"
	@$(COMPOSE) up -d $(SERVICE) >/dev/null
	@$(MAKE) wait
	@echo "Integration checks passed (health + networks + leases + collision + SIGTERM)."

lint:
	@go vet ./...
	@gofmt -l . | tee /tmp/forge-network-gofmt.out | grep -q . && exit 1 || true

format:
	gofmt -w .

docker-build:
	docker build -t $(IMAGE) .
