#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

echo "== Demo 00: Repository foundation =="

make setup
make dev
./scripts/smoke-test.sh

echo
echo "Foundation demo passed."
echo "Grafana:     http://localhost:3000"
echo "Prometheus:  http://localhost:3001"
echo "Tempo:       http://localhost:3002"
echo "Loki:        http://localhost:3003"
echo "Registry:    http://localhost:5000/v2/"
echo "PostgreSQL:  localhost:5001"
echo "NATS:        localhost:5002 (monitor :5003)"
