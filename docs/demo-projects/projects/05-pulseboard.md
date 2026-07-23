# Demo 5 — PulseBoard

**Epic:** [`55-demo-pulseboard`](../../implementation/epics/55-demo-pulseboard.md) · **Focus:**
**autoscaling under load** (HTTP request-rate → replicas, then **node** scale-up via
Infrastructure) and **observability** surfacing.

A small **live metrics dashboard**. The product shows a real-time counter/leaderboard and — the
point of the demo — its **own replica count and platform metrics**, read from Observe. A load
generator drives traffic; you watch the API **scale out** in the browser and in Grafana, then
**scale back in** when load stops. If replicas can't be placed, **Infrastructure adds a Docker
node**.

---

## 1. Why this product

TaskFlow proves steady-state serving; PulseBoard proves the platform reacts to load. It closes the
loop: request-rate autoscaling of a workload, node autoscaling when the cluster runs out of room,
and observability good enough that the product itself can display platform state. It's the
user-visible face of epics 23/24 (already gated internally by `demos/24-autoscaling`) tied to a
real app and browser.

## 2. Services exercised

| Service | How PulseBoard uses it | Proven by |
|---|---|---|
| forge-autoscaler | `ScalingPolicy { type: httpRequests }` on the API → replicas track RPS; scale-down after. | Replicas rise under load, fall after. |
| forge-infrastructure | When desired replicas exceed capacity, a Docker node is provisioned; drained when idle. | Node count rises then falls. |
| forge-observe | API emits metrics/traces/logs; dashboard queries Observe for RPS, p95, replica count. | Dashboard values match Grafana. |
| forge-gateway | Routes traffic + is the request-rate metric source for the autoscaler. | Host preflight; RPS metric flows. |
| forge-discovery (light) | Dashboard discovers the API/metrics endpoint. | Endpoint resolves. |
| control/cli/runtime/build | Baseline deploy + scaling actuation. | Replicas actuated by Control reconcile. |

## 3. Architecture

```text
Browser ──▶ Gateway :4000  board.pulseboard.localhost ─▶ pulseboard-web (live dashboard)
                           api.pulseboard.localhost  ─▶ pulseboard-api (Go)  [autoscaled 1..N]

load generator ─▶ Gateway ─▶ pulseboard-api (RPS ↑)
   Gateway RPS metric ─▶ forge-autoscaler ─▶ desiredReplicas ↑ ─▶ Control reconcile ─▶ Runtime
       if unschedulable ─▶ forge-autoscaler node path ─▶ forge-infrastructure creates Docker node
pulseboard-web polls ─▶ pulseboard-api /stats ─▶ forge-observe (replica count, RPS, p95)
```

The dashboard reading platform metrics is what makes autoscaling **watchable in a browser**, not
just in Grafana.

## 4. Manifests (illustrative — `55.02`/`55.03`)

```yaml
kind: Application            # pulseboard-api
spec:
  image: registry.forge.internal/pulseboard/pulseboard-api:latest
  resources: { cpu: 250m, memory: 128Mi }
  scaling:
    minReplicas: 1
    maxReplicas: 10
    policies:
      - { type: httpRequests, targetRequestsPerSecond: 50 }
  routes:
    - { host: api.pulseboard.localhost, path: /, healthPath: /health/ready }
---
# operator-owned; enables the node scale-up leg
kind: NodePool
metadata: { name: pulseboard-pool }
spec:
  provider: docker
  scaling: { minNodes: 1, maxNodes: 3 }
```

## 5. "Data" model

PulseBoard is mostly stateless (no Postgres required). It keeps an in-memory/Redis-less counter and
derives everything else from Observe. Keeping it DB-free keeps the demo focused on scaling +
observability. (Persistence is already covered by TaskFlow/SnapNote/AskDocs/OrderPipe.)

## 6. E2E scenario (`tests/e2e/projects/05-pulseboard/spec.ts`)

1. Open `board.pulseboard.localhost` → dashboard shows **replicas: 1**, low RPS.
2. **Start load** (harness-driven generator hits `api.pulseboard.localhost`).
3. Watch the dashboard **replica count climb** (and RPS/p95 rise); cross-check the same replica
   number in Grafana via Observe. Assert replicas increased and stayed ≤ `maxReplicas`.
4. **Capacity leg (optional/threshold):** push load high enough that replicas can't be placed →
   assert **node count increases** (Infrastructure provisioned a Docker node) and the extra
   replicas become Running.
5. **Stop load** → after stabilization, dashboard shows replicas **scaling back down** toward
   `minReplicas`; idle node drains and is removed.

Assertions check **direction and bounds** (monotone-ish up, then down; within `[min,max]` /
`[minNodes,maxNodes]`), never exact counts or exact timing — see harness §9.

### Platform assertions (→ findings)
* Gateway request-rate metric reaches the autoscaler; `ScalingPolicy.status` reflects it.
* Control reconcile actuates the recommended replica count; Runtime runs them.
* Node scale-up only fires on genuine unschedulability and scale-down respects drain safeguards.
* Observe exposes replica count / RPS / p95 consistently between the dashboard and Grafana.

## 7. Likely findings hotspots

Metric freshness/lag from Gateway/Observe into the autoscaler, stabilization-window/flapping
behaviour, node scale-up latency and drain safety, replica-count consistency across
autoscaler/Control/Observe, scale-to-min correctness.

## 8. Acceptance criteria

* `make demo DEMO=55` + `05-pulseboard` E2E pass headed and headless.
* Under load, API replicas scale up within bounds and are visible in the browser and Grafana.
* When capacity is exceeded, a Docker node is added and later drained.
* On load stop, replicas (and any added node) scale back down.
* Zero blocker findings attributed to PulseBoard.

## 9. Open question

If **epic 25 (scheduling enhancements)** ships user-visible behaviour, extend PulseBoard with a
scheduled-scaling panel (`scaling.schedules`) rather than adding a sixth product. Tracked in
[epic 55](../../implementation/epics/55-demo-pulseboard.md).

## 10. Steps → see epic

`55.01` scaffold + baseline deploy · `55.02` HTTP request-rate autoscaling + load gen · `55.03`
node autoscaling (Infrastructure) · `55.04` Observe surfacing (dashboard reads platform metrics) ·
`55.05` E2E browser spec · `55.06` demo + gate. Details:
[epic 55](../../implementation/epics/55-demo-pulseboard.md).
