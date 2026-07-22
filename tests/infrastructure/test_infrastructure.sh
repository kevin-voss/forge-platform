#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

if [[ ! -f .env ]]; then
  cp .env.example .env
fi

echo "== Ensuring infrastructure is running =="
docker compose up -d

echo "== Waiting for services =="
./scripts/wait-for-service.sh http://127.0.0.1:5003/healthz 120
./scripts/wait-for-service.sh http://127.0.0.1:5000/v2/ 120
./scripts/wait-for-service.sh http://127.0.0.1:13133/ 120
./scripts/wait-for-service.sh http://127.0.0.1:3001/-/healthy 120
./scripts/wait-for-service.sh http://127.0.0.1:3002/ready 120
./scripts/wait-for-service.sh http://127.0.0.1:3003/ready 120
./scripts/wait-for-service.sh http://127.0.0.1:3000/api/health 120

echo "== Waiting for PostgreSQL =="
for i in $(seq 1 120); do
  if docker compose exec -T postgres pg_isready -U forge -d forge >/dev/null 2>&1; then
    echo "Ready: PostgreSQL"
    break
  fi
  if [[ "${i}" -eq 120 ]]; then
    echo "Timed out waiting for PostgreSQL" >&2
    exit 1
  fi
  sleep 1
done

echo "== Running smoke checks =="
./scripts/smoke-test.sh

echo "== Verifying PostgreSQL query =="
docker compose exec -T postgres psql -U forge -d forge -c "SELECT id FROM forge.schema_migrations WHERE id = '00-foundation';" | grep -q 00-foundation

echo "Infrastructure tests passed."
