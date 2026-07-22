# Epic 04: Forge Runtime

## Status

In progress

## Goal

Deliver a single-node container runtime in Rust that turns a desired deployment into a running Docker container and reports actual state back to Control. When this epic is done, a deployment created via Control/CLI can be materialized by Runtime: it registers the local node, pulls the image, injects environment and port mappings with deterministic names/labels, probes health, streams logs, and stops/deletes cleanly — with idempotent operations that never create duplicate containers.

## Why this epic exists

Control (02) only records intent; nothing runs. Runtime is the component that executes workloads on a node using Docker Engine, closing the loop from desired state to actual state. It is the prerequisite for Gateway routing (05), source-to-deploy (06), reconciliation (07), and multi-node scheduling (08).

## Primary code areas

* `services/forge-runtime/` — Rust service, Docker Engine adapter, workload lifecycle, HTTP API (port `4102`)
* `demos/04-runtime/` — deploy the Go demo image end-to-end

## Suggested language

Rust (per `specs.md` §4). Async runtime (Tokio) + an HTTP framework (Axum) + a Docker Engine client (bollard or the Docker HTTP API over the socket). Docker Engine is the execution backend — containers are **not** implemented from scratch.

## Spec references

* `specs.md` → Step 04: Forge Runtime
* `specs.md` → §4 Language matrix (Rust for Runtime)
* `specs.md` → §2.2 Containers are the runtime boundary
* Epic [`01-runtime-contract`](01-runtime-contract.md) → health/identity/log conventions Runtime both honors (as a service) and probes (on workloads)
* Epic [`02-forge-control`](02-forge-control.md) → deployment desired-state API and status reporting

## Dependencies

* Epic [`02-forge-control`](02-forge-control.md) — deployment read models / desired state (`02.05`) and a place to report actual state (`04.07` integrates)
* Epic [`01-runtime-contract`](01-runtime-contract.md) — the Go demo image (`01.03`) used as the deploy target; workload health/identity contract
* Epic [`03-forge-cli`](03-forge-cli.md) — optional, used to drive the demo (`03.03`)
* Epic `00` — Docker Engine available, local registry `localhost:5000`, Compose stack

## Out of scope for this epic

* Multi-node scheduling / placement (epic 08)
* Rolling updates, automatic rollback, reconciliation loop (epic 07)
* Gateway routing to workloads (epic 05)
* Secrets injection beyond plain env (epic 10)
* Building images from source (epic 06)

## Success demo

```bash
make demo DEMO=04
```

`demos/04-runtime` deploys the Step-01 Go demo image via the Control→Runtime path: the container starts, becomes ready, the deployment status flips to `active`, logs are readable through Runtime, and deleting the deployment removes the container. Duplicate deploy commands do not create duplicate containers.

```text
Forge CLI → Forge Control → Forge Runtime → Docker Engine → demo-go container
                                   │
                          reports actual state ↑
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [04.01](../steps/04-forge-runtime/04.01-skeleton-docker-socket-health.md) | Skeleton + Docker socket + health | Complete | Rust/Axum, port 4102, Docker ping |
| [04.02](../steps/04-forge-runtime/04.02-node-identity-registration-heartbeat.md) | Node identity + registration/heartbeat | Complete | Stable node id, `/v1/node`, heartbeat, label helper |
| [04.03](../steps/04-forge-runtime/04.03-workload-create-start.md) | Workload create/start (pull, env, ports, labels) | Complete | Pull/create/start + host port + labels |
| [04.04](../steps/04-forge-runtime/04.04-health-probing-status-model.md) | Health probing + status model | Complete | Prober + normalized status + `/status` API |
| [04.05](../steps/04-forge-runtime/04.05-log-streaming.md) | Log streaming | Complete | Bounded + SSE follow via Docker logs |
| [04.06](../steps/04-forge-runtime/04.06-stop-delete-no-duplicates.md) | Stop/delete; no duplicate containers | Complete | Idempotent POST + graceful DELETE |
| [04.07](../steps/04-forge-runtime/04.07-control-integration.md) | Control integration (desired→actual) | Complete | Poll Control, converge, `/v1/node/state`, status push contract |
| [04.08](../steps/04-forge-runtime/04.08-demo-runtime-and-gate.md) | Demo `04-runtime` + gate | Not started | Depends on all prior |

## Assumptions

* Runtime source lives under `services/forge-runtime/`; demo under `demos/04-runtime/`.
* Runtime listens on host port `4102` (internal range); in-container `PORT` default `8080`.
* Docker access is via the mounted Docker socket (`/var/run/docker.sock`) — documented as a privileged dev convenience.
* Container names/labels are deterministic and derived from `deployment_id` (e.g. name `forge-<deploymentId>`, labels `forge.deployment_id`, `forge.node_id`, `forge.managed=true`).
* Workload port is published to an ephemeral host port; the mapping is reported to Control for Gateway (epic 05) to consume.
* Until Identity `09.06`, Runtime↔Control calls use `FORGE_AUTH_MODE=dev`.
* One node only in this epic; node identity is stable across restarts (persisted node id).

## Open questions

* **Node id persistence:** file on disk vs derived from hostname/machine-id? (Assumption: generate once, persist to a data dir.)
* **Actual-state reporting:** does Runtime push status to Control, does Control poll Runtime, or both? (Assumption: Runtime pushes on change + Control can GET current state in `04.07`.)
* **Host port allocation:** let Docker pick an ephemeral port and read it back, or manage a range? (Assumption: Docker-assigned ephemeral, read back and report.)
* **Image pull auth:** local registry is anonymous in dev; private registry auth deferred.
* **Socket security:** mounting the Docker socket is powerful; acceptable for local dev, flagged for hardening later.

## Next step to implement

**[04.08](../steps/04-forge-runtime/04.08-demo-runtime-and-gate.md) — Demo `04-runtime` + gate**.
