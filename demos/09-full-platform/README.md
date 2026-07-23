# Demo 09 — Full platform (capstone)

Polyglot incident-management product deployed through the real Forge path, with
platform foundations wired (Identity, Secrets, Observe, Storage, managed DB):

```text
Identity (enforce) → Control authz (viewer denied / developer deploy)
Secrets            → APP_SHARED_SECRET + PRODUCT_MODE + DATABASE_URL injection
Observe/OTEL       → distributed trace across api + admin + classify
Storage            → project bucket artifact via incident-api
Managed Postgres   → forge database create/attach → incidents table
```

This folder is the **thematic** north-star demo (`demos/09-full-platform`). It is
**not** the Identity epic demo (`demos/09-platform-identity`).

## Status (epic 19)

| Step | Scope |
|---|---|
| **19.01** | Product services under `product/` (complete) |
| **19.02** | Deploy path Build→Runtime→Gateway→Events (complete) |
| **19.03** | Identity / Secrets / Observe / Storage / managed DB (**this README**) |
| 19.04+ | Models/Agents/Memory, failure/rollback, acceptance suite |

## Auth

`FORGE_AUTH_MODE=enforce` for Control + Secrets. Product API requires a Bearer
PAT (Identity introspect). Role difference:

* **developer** PAT → `forge deployment create` succeeds
* **viewer** PAT → Control deploy returns **403 forbidden**

## Gateway hostnames

See [`routes.md`](routes.md). Quick check (health stays open):

```bash
curl -fsS -H 'Host: api.demo.localhost' http://127.0.0.1:4000/health/ready
```

Authenticated product calls need `Authorization: Bearer <developer PAT>`.

| Host | Service |
|---|---|
| `api.demo.localhost` | incident-api |
| `admin.demo.localhost` | incident-admin |
| `logs.demo.localhost` | incident-log-worker |
| `classify.demo.localhost` | incident-classify |
| `notify.demo.localhost` | incident-notify |

## Deploy + foundations

```bash
cd demos/09-full-platform
./deploy.sh
```

What `deploy.sh` does:

1. Starts platform services (Identity, Secrets, Control LocalProvisioner, Runtime,
   Gateway, Build, Events, Observe, Storage, OTEL/Tempo)
2. Registers owner/org; creates Control project; issues developer + viewer PATs
3. Asserts viewer cannot deploy; developer can
4. Runs [`setup-foundations.sh`](setup-foundations.sh): Secrets bindings,
   `forge database create/attach`, Storage bucket
5. Builds + deploys all five product services via **`forge deployment create`**
6. Asserts: DB status, secret status (no plaintext), Storage artifact round-trip,
   Tempo distributed trace across ≥3 product services, log masking

Unit tests:

```bash
python3 -m unittest discover -s lib -p 'test_*.py' -v
cd product/api-go && go test ./...
```

## Product endpoints (api-go)

| Path | Notes |
|---|---|
| `/db-status` | `DATABASE_URL_present` + backend; never echoes URL |
| `/secret-status` | `APP_SHARED_SECRET_present` + length; never echoes secret |
| `/incidents` | Persisted in managed Postgres when `DATABASE_URL` injected |
| `/artifacts` | Upload/download via Forge Storage (project-scoped) |

## Contracts used

Documented platform APIs only: Identity auth/tokens, Secrets set/bindings,
Control hierarchy + deployments + managed DB, Runtime injection, Gateway proxy,
Events publish/consume, Observe/Tempo traces, Storage buckets/objects. CLI:
`forge login|project|env|app|service|deployment|secret|config|database`.
