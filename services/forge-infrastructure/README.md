# forge-infrastructure

Go service (host port **4111**) that turns declared `NodePool` resources into real machines via pluggable provider adapters.

## Step 23.01 (current)

* Health: `GET /health/live`, `GET /health/ready`
* Debug: `GET /v1/operations/{opId}` (operation ledger)
* `Provider` interface (16 methods) + registry + `noop` default (`ErrProviderNotConfigured`)
* Cluster-scoped kinds registered with Control: `InfrastructureProvider`, `NodePool`, `Node`
* `provider_operations` ledger (`op_<ULID>`) for idempotent mutating calls
* Inert `NodePoolController` (waits for a real adapter in 23.02)

## Configuration

| Env | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | In-container listen port (host `4111`) |
| `FORGE_INFRA_DB_URL` / `FORGE_DATABASE_URL` | local Postgres | Ledger DB |
| `FORGE_DATABASE_SCHEMA` | `infrastructure` | Schema for ledger |
| `FORGE_REGISTRY_URL` | `http://forge-control:8080` | Epic-20 resource API |
| `FORGE_INFRA_RECONCILE_INTERVAL_MS` | `2000` | NodePool reconcile period |
| `FORGE_AUTH_MODE` | `dev` | Service-to-service until mTLS |

## Local commands

```bash
make -C services/forge-infrastructure test-unit
make -C services/forge-infrastructure run
curl -sf http://127.0.0.1:4111/health/ready
```
