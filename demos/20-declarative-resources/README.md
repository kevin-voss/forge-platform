# Demo 20: Declarative resource API (epic gate)

End-to-end acceptance gate for epic 20. Proves Forge accepts a portable
`forge.dev/v1` Application manifest, records generation changes, emits watch
events, reconciles the companion Deployment via the epic-07 controller, and
reaches Application `Ready` — without any provider-specific fields.

```text
forge apply -f application.yaml
  → Application invoice-api generation=1
  → watch emits ADDED
  → reconciler drives Deployment → deploying → active
  → application-controller status → observedGeneration=1, phase=Ready
forge apply -f application-update.yaml
  → generation=2
  → watch emits MODIFIED + STATUS_MODIFIED
```

Legacy project/application/deployment routes remain unchanged (smoke-checked).

This demo sets `FORGE_AUTH_MODE=dev` (Control defaults to `enforce` as of `09.06`).
Runtime mounts the host Docker socket — local-dev only.

## Run

From the repository root:

```bash
make demo DEMO=20
```

Expect a final `demo 20 PASSED` line and exit code `0`.

## What this demo checks

* Portable multi-doc apply (Project → Environment → Application → Service → Deployment)
* Generation bumps on Application image update
* SSE watch `ADDED` / `MODIFIED` / `STATUS_MODIFIED` (live + replay)
* Label listing (`labelSelector=demo=20`)
* Deployment reconcile to `active` through Runtime readiness
* Application `/status` convergence (`observedGeneration`, `Ready`)
* Stale `resourceVersion` → `409`
* Provider-specific field rejected (`portable_manifest_violation`)
* Legacy `/v1/projects` / applications / deployments shapes still work

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control readiness + resource API |
| `FORGE_RUNTIME_URL` | `http://127.0.0.1:4102` | Runtime readiness |
| `FORGE_ENDPOINT` | same as Control | CLI profile endpoint |
| `FORGE_AUTH_MODE` | `dev` | Insecure bypass for this gate |
| `FORGE_LIFECYCLE_OWNER` | `control` | Control creates/stops workloads |
| `FORGE_RECONCILE_INTERVAL_MS` | `1000` | Faster reconcile ticks |

`docker-compose.yml` in this directory is an overlay on the root `compose.yaml`.

## Images

Built from `apps/demo/` (same contract as demo 07):

| Tag | `VERSION` |
|---|---|
| `localhost:5000/demo-declarative:v1` | `v1` |
| `localhost:5000/demo-declarative:v2` | `v2` |

## Docs

Portable Application manifests are the recommended future entry point — see
[`docs/concepts/application-manifest.md`](../../docs/concepts/application-manifest.md).
