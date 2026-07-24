# Service coverage matrix

The completeness contract for the demo track: **every** Forge service must be exercised by at
least one demo product through real product usage (not a synthetic smoke call). Epic
[`56.02`](../implementation/steps/56-platform-e2e-gate/56.02-coverage-verification.md) fails the
suite if any service drops to zero coverage.

Legend: **●** primary focus (the demo is designed to prove this service) · **○** used as a
supporting dependency · blank = not used.

| Service | Port | 1 TaskFlow | 2 SnapNote | 3 AskDocs | 4 OrderPipe | 5 PulseBoard | How it is proven |
|---|---|:--:|:--:|:--:|:--:|:--:|---|
| forge-control | 4001 | ○ | ○ | ○ | ○ | ○ | Every product deploys via Control (`forge apply` / deployment create). |
| forge-cli | — | ○ | ○ | ○ | ○ | ○ | Harness drives all deploys through the `forge` CLI. |
| forge-runtime | 4102 | ○ | ○ | ○ | ○ | ○ | Containers run on Runtime nodes; health/readiness gates. |
| forge-gateway | 4000 | ● | ○ | ○ | ● | ○ | Host-based routing to every product UI + API; multi-service routing in OrderPipe. |
| forge-build | 4103 | ● | ○ | ○ | ○ | ○ | Build product images from source via `forge build` (TaskFlow proves source→image). |
| forge-identity | 4002 | ● | ○ | | ○ | | Signup/login, PAT issuance, roles (developer vs viewer; app roles admin/member). |
| forge-secrets | 4104 | ● | ○ | ○ | ● | | Inject DB creds, JWT keys, API keys — never plaintext in manifests. |
| forge-events | 4105 | | ● | ○ | ● | | Durable queue + pub/sub: attachment jobs (SnapNote), order events (OrderPipe). |
| forge-observe | 4106 | ○ | ○ | ○ | ○ | ● | Traces/logs/metrics; PulseBoard surfaces them; every demo asserts telemetry exists. |
| forge-storage | 4107 | | ● | ● | | | S3-style object storage: attachments (SnapNote), documents (AskDocs). |
| forge-discovery | 4109 | | | | ● | ○ | Service-to-service resolution via `.svc.forge` DNS (OrderPipe multi-service). |
| forge-network | 4110 | | | | ● | | Overlay + NetworkPolicy allow/deny between OrderPipe services. |
| forge-infrastructure | 4111 | | ○ | | | ● | Node provisioning (Docker provider) when workloads exceed capacity. |
| forge-autoscaler | 4112 | | ● | | | ● | Worker queue-depth scaling (SnapNote); HTTP request-rate + node scaling (PulseBoard). |
| forge-models | 4300 | | | ● | | | Embeddings + completion (deterministic fake backend) for RAG. |
| forge-agents | 4301 | | | ● | | | Agent with a retrieval tool produces grounded answers. |
| forge-workflows | 4302 | | | | ● | | Order saga: validate→charge→fulfill→notify with retry/compensation. |
| forge-memory | 4303 | | | ● | | | Vector store for document chunks; semantic retrieval. |
| managed PostgreSQL | 5001 | ● | ○ | ○ | ○ | | Managed `Database` dependency; migrations + app data (TaskFlow is the reference). |
| Declarative API (`forge apply`) | 4001 | ○ | ○ | ○ | ○ | ● | Application/Route/Queue/ScalingPolicy resources applied and watched to `Ready`. |

## Coverage summary by service

Each service below names its **primary** demo (the one whose acceptance directly fails if the
service misbehaves), so a red service maps to a specific demo to debug first.

| Service | Primary demo | Secondary demos |
|---|---|---|
| Gateway | TaskFlow, OrderPipe | all |
| Build | TaskFlow | all |
| Identity | TaskFlow | OrderPipe |
| Secrets | TaskFlow, OrderPipe | SnapNote, AskDocs |
| Managed Postgres | TaskFlow | SnapNote, AskDocs, OrderPipe |
| Storage | SnapNote, AskDocs | — |
| Events (queue) | SnapNote, OrderPipe | AskDocs |
| Autoscaler (worker) | SnapNote | — |
| Autoscaler (HTTP + node) | PulseBoard | SnapNote (node) |
| Infrastructure | PulseBoard | SnapNote |
| Observe | PulseBoard | all |
| Models | AskDocs | — |
| Memory | AskDocs | — |
| Agents | AskDocs | — |
| Workflows | OrderPipe | — |
| Discovery | OrderPipe | PulseBoard |
| Network | OrderPipe | — |
| Control / CLI / Runtime | all | — |

## Verification notes

* **Epic 51 (TaskFlow) Complete:** column **1 TaskFlow** exercised end-to-end via
  `make demo DEMO=51` / `make test-platform-e2e PROJECTS=01` (headed + headless). Coverage tokens
  from `demos/51-taskflow/demo.json` (`control`, `cli`, `runtime`, `gateway`, `build`, `identity`,
  `secrets`, `managed-postgresql`, `observe`, `apply`). Non-blocker findings `F-001`–`F-004` remain
  open in [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md); zero blockers.
* **Epic 52 (SnapNote) Complete:** column **2 SnapNote** exercised end-to-end via
  `make demo DEMO=52` / `make test-platform-e2e PROJECTS=02` (headed + headless). Coverage tokens
  from `demos/52-snapnote/demo.json` (`control`, `cli`, `runtime`, `gateway`, `build`,
  `managed-postgresql`, `storage`, `events`, `autoscaler`). Non-blocker finding `F-005` remains
  open in [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md); zero blockers.
* **Epic 53 (AskDocs) Complete:** column **3 AskDocs** exercised end-to-end via
  `make demo DEMO=53` / `make test-platform-e2e PROJECTS=03` (headed + headless). Coverage tokens
  from `demos/53-askdocs/demo.json` (`control`, `cli`, `runtime`, `gateway`, `build`,
  `managed-postgresql`, `storage`, `events`, `models`, `memory`, `agents`, `observe`).
  Non-blocker findings `F-006`–`F-007` remain open in
  [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md); zero blockers.

## Deliberate gaps (recorded, not accidental)

* **Hetzner/AWS/Azure infrastructure providers** are covered by the platform's own epic-23 demo,
  not re-tested here — this track is local Docker only. Manifests stay provider-neutral so the
  same products *could* deploy there.
* **Scheduling enhancements (epic 25)** are surfaced indirectly through PulseBoard placement but
  are not given a dedicated product; if epic 25 ships user-visible behaviour, add a 6th demo or
  extend PulseBoard (tracked as an open question in [55-demo-pulseboard](../implementation/epics/55-demo-pulseboard.md)).
