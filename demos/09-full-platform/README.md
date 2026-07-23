# Demo 09 — Full platform (capstone)

Polyglot incident-management product deployed through the real Forge path:

```text
source → Build → registry → forge deployment create → Runtime → Gateway
incident-api ──(Events: incident.created)──► incident-log-worker
```

This folder is the **thematic** north-star demo (`demos/09-full-platform`). It is
**not** the Identity epic demo (`demos/09-platform-identity`).

## Status (epic 19)

| Step | Scope |
|---|---|
| **19.01** | Product services under `product/` (complete) |
| **19.02** | Deploy path Build→Runtime→Gateway→Events (**this README**) |
| 19.03+ | Identity/Secrets/Observe/Storage/DB, AI, failure/rollback, acceptance suite |

## Auth note (temporary)

`FORGE_AUTH_MODE=dev` (and Events `FORGE_EVENTS_AUTH_MODE=dev`) until **19.03**
wires Identity. No hardcoded secrets.

## Gateway hostnames

See [`routes.md`](routes.md). Quick check (no DNS required):

```bash
curl -fsS -H 'Host: api.demo.localhost' http://127.0.0.1:4000/health/ready
```

| Host | Service |
|---|---|
| `api.demo.localhost` | incident-api |
| `admin.demo.localhost` | incident-admin |
| `logs.demo.localhost` | incident-log-worker |
| `classify.demo.localhost` | incident-classify |
| `notify.demo.localhost` | incident-notify |

## Deploy path

```bash
cd demos/09-full-platform
./deploy.sh
```

What `deploy.sh` does:

1. Starts platform services via root `compose.yaml` + this overlay (`compose.yaml`)
2. Creates Control project / env / app / five services
3. Submits each product directory to **Forge Build** (`file:///fixtures/product/...`)
4. On success, runs **`forge deployment create`** (CLI deploy path) with the registry image
5. Waits for Runtime `active` + Gateway routes
6. Creates an incident through Gateway and asserts `incident.created` is consumed by `logs`

Unit tests for deploy helpers:

```bash
python3 -m unittest discover -s lib -p 'test_*.py' -v
```

## Events

* Schema: `contracts/events/incident.created.schema.json`
* Stream family: `incident` (overlay sets `FORGE_EVENTS_STREAMS=…,incident`)
* Producer: `product/api-go` publishes on `POST /incidents`
* Consumer: `product/log-worker-rust` durable consumer → `GET /events/status`

Runtime workloads default `FORGE_EVENTS_URL=http://host.docker.internal:4105` when
`FORGE_DEPLOYMENT_ID` is present (host-published Events port).

## Product-only smoke (no platform)

```bash
cd product && docker compose up -d --build && ./run.sh
```

## Contracts used

Documented platform APIs only: Build `/v1/builds`, Control hierarchy + deployments,
Runtime convergence, Gateway routes/proxy, Events publish/consume/ack. CLI:
`forge project|env|app|service|deployment create`.
