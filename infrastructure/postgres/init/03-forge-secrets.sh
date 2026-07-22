#!/bin/bash
# Dedicated database for forge-secrets (epic 10 / step 10.01).
# Runs only on first Postgres volume init.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-forge}" --dbname "${POSTGRES_DB:-forge}" <<-EOSQL
	SELECT 'CREATE DATABASE forge_secrets'
	WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_secrets')\gexec
EOSQL
