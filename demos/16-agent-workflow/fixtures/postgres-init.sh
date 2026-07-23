#!/bin/bash
# Demo 16 Postgres bootstrap — forge_workflows (+ forge_events reserved).
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-forge}" --dbname "${POSTGRES_DB:-forge}" <<-EOSQL
	SELECT 'CREATE DATABASE forge_workflows'
	WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_workflows')\gexec
	SELECT 'CREATE DATABASE forge_events'
	WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_events')\gexec
EOSQL
