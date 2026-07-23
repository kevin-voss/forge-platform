# Demo 25: HA placement (M1 exit gate)

End-to-end acceptance gate for epic 25 and the **M1 standalone-cloud** exit.
Proves a portable Application with anti-affinity, topology domains, requests,
priority class, and a disruption budget spreads across distinct Docker-provider
nodes/zones, publishes Ready Discovery endpoints, routes via Gateway, and
recovers after simulated node loss.

```text
Apply portable Application manifest
  → Infrastructure creates 3+ runtime nodes
  → Scheduler spreads replicas across distinct nodes / 2 zones
  → Discovery publishes Ready endpoints
  → Gateway routes to healthy replicas
  → stop one runtime node
  → stale endpoints leave Ready set; replica rescheduled
  → disruption budget remains satisfied
```

Compose builds are sequential (`COMPOSE_PARALLEL_LIMIT=1`) for memory-constrained
hosts.

## Run

From the repository root:

```bash
make demo DEMO=25
```

Expect a final `demo 25 PASSED` line and exit code `0`.

## What this demo checks

* Product manifest has no provider-specific IDs, networks, disks, or machine types
* Three replicas land on three distinct provider nodes spanning `zone-a` / `zone-b`
* Discovery reports Ready endpoints for the workload
* Gateway has healthy upstreams and serves the routed host
* Stopping one runtime node marks it offline, reschedules the lost replica, and
  keeps `min_available=2` satisfied after recovery
* Autoscaler is online so pending demand can grow the NodePool if capacity is short

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control API |
| `FORGE_INFRA_URL` | `http://127.0.0.1:4111` | Infrastructure health |
| `FORGE_DISCOVERY_URL_HOST` | `http://127.0.0.1:4109` | Discovery API (host) |
| `FORGE_GATEWAY_URL` | `http://127.0.0.1:4000` | Gateway |
| `FORGE_AUTH_MODE` | `dev` | Insecure bypass for this gate |
| `COMPOSE_PARALLEL_LIMIT` | `1` | Sequential Compose builds |
| `FORGE_ANTI_AFFINITY_DEFAULT` | `hard` | Distinct-node placement for HA |
| `FORGE_DEMO25_GPU` | unset | Optional GPU assertion hook |

## Fixtures

* `fixtures/nodepool-docker.yaml` — operator `InfrastructureProvider` + `NodePool`
* `fixtures/application.yaml` — portable Project/Environment/Application/Service/Deployment

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Fewer than 3 Ready nodes | Docker Desktop memory / image pull | Stop other stacks; rebuild `forge-runtime` |
| Spread assertion fails (1 zone) | Zone SQL not preserved | Confirm agents send `zone=default` (preserve path) |
| Discovery Ready stuck at 0 | Provider nodes missing `FORGE_DISCOVERY_URL` | Rebuild `forge-infrastructure`; overlay injects Discovery env |
| Gateway no upstreams | Route sync lag / reconcile not deployed | Check `POST /admin/routes/refresh` and Control reconcile |
| `make demo` `Killed: 9` | Host/Docker OOM (~8Gi) | Sequential Compose; Control `-Xmx384m` |

## Docs

* Epic: [`docs/implementation/epics/25-scheduling-enhancements.md`](../../docs/implementation/epics/25-scheduling-enhancements.md)
* Step: [`docs/implementation/steps/25-scheduling-enhancements/25.06-demo-25-ha-placement.md`](../../docs/implementation/steps/25-scheduling-enhancements/25.06-demo-25-ha-placement.md)
* Portability: [`docs/concepts/application-manifest.md`](../../docs/concepts/application-manifest.md)
