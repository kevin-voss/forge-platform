# Capstone product (incident management)

Polyglot sample product for epic `19` / `demos/09-full-platform`. This folder is
**product-only**: five runtime-contract-compliant services with local Compose smoke
checks. Platform deploy, Identity/Secrets/Observe wiring, and the north-star recovery
scenario arrive in later `19.xx` steps.

## Services

| Directory | Service name | Language | Host port | Role |
|---|---|---|---:|---|
| `api-go/` | `incident-api` | Go | 4211 | Public incident CRUD entrypoint |
| `admin-kotlin/` | `incident-admin` | Kotlin | 4212 | Admin/config surface |
| `log-worker-rust/` | `incident-log-worker` | Rust | 4213 | Log ingest/processing |
| `classify-python/` | `incident-classify` | Python | 4214 | Deterministic classification |
| `notify-elixir/` | `incident-notify` | Elixir | 4215 | Notification worker |

Each service exposes the epic-01 runtime contract:

* `GET /health/live`
* `GET /health/ready`
* `GET /` identity (`service`, `language`, `status`, …)
* Structured JSON logs to stdout (`timestamp`, `level`, `service`, `message`)
* `Dockerfile` + `forge.yaml`
* Graceful SIGTERM shutdown

## Product API sketch (local / future Gateway)

Contract-level only in this step — services are not yet wired through Gateway/Events.

```text
incident-api
  POST   /incidents              create incident (in-memory)
  GET    /incidents              list
  GET    /incidents/{id}         get by id

incident-admin
  GET    /admin/config           read admin config
  PUT    /admin/config           update admin config (in-memory)

incident-log-worker
  POST   /logs                   ingest a log entry
  GET    /logs                   list processed entries

incident-classify
  POST   /classify               { "text": "..." } → stable label

incident-notify
  POST   /notify                 queue a notification
  GET    /notifications          list queued notifications
```

### Intended interactions (later steps)

```text
Client → Gateway → incident-api
incident-api ──(Events)──► incident-log-worker
incident-api ──(HTTP/Gateway)──► incident-classify
incident-api / workflows ──(Events)──► incident-notify
Operators → Gateway → incident-admin
```

Persistence, secrets, and auth are **not** attached yet (see `19.03`). Classification is
deterministic (keyword rules + stable hash) so later acceptance assertions stay fixed.

## Configuration

Common env (all services):

| Variable | Purpose |
|---|---|
| `PORT` | Listen port (required; containers default `8080`) |
| `FORGE_SERVICE_NAME` | Identity + log `service` field |
| `FORGE_SERVICE_VERSION` | Identity version |
| `FORGE_LOG_LEVEL` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | Environment label |

No hardcoded secrets. Placeholders for later injection (`DATABASE_URL`, Identity tokens,
etc.) will be documented when managed DB / Secrets land in `19.03`.

## Local smoke

```bash
cd demos/09-full-platform/product
docker compose up -d --build
./run.sh   # readiness + contract-validator for all five
```

Manual checks:

```bash
curl -fsS localhost:4211/health/ready
curl -fsS localhost:4212/
curl -fsS -XPOST localhost:4214/classify -H 'content-type: application/json' \
  -d '{"text":"Deploy rollback failed after canary"}'
```

## Data model (product-local)

* **Incident** — `id`, `title`, `description`, `severity`, `status`, `created_at` (in-memory in `api-go`)
* **Classification** — `label`, `confidence`, `reason` (stateless / deterministic)
* **AdminConfig** — notify flag, default severity, retention days
* **LogEntry** / **Notification** — in-memory queues for smoke runs

Managed Postgres persistence is deferred to `19.03`.
