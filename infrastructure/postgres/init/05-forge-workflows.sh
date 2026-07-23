#!/bin/bash
# Dedicated database for forge-workflows (epic 16 / step 16.02).
# Runs only on first Postgres volume init.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-forge}" --dbname "${POSTGRES_DB:-forge}" <<-EOSQL
	SELECT 'CREATE DATABASE forge_workflows'
	WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_workflows')\gexec
EOSQL
