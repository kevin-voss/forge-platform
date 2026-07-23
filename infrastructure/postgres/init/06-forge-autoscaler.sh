#!/bin/bash
# Dedicated database for forge-autoscaler (epic 24 / step 24.01).
# Runs only on first Postgres volume init.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-forge}" --dbname "${POSTGRES_DB:-forge}" <<-EOSQL
	SELECT 'CREATE DATABASE forge_autoscaler'
	WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'forge_autoscaler')\gexec
EOSQL
