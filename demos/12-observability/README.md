# Demo 12: Observability (one distributed trace)

End-to-end acceptance gate for epic 12 (Forge Observe). The script brings up
the foundation OTEL stack (collector, Tempo, Loki, Prometheus, Grafana,
Alertmanager), Forge Observe (`4106`), and the instrumented Control / Build /
Runtime / Gateway services, deploys an OTEL-enabled demo app, then proves:

```text
CLI → Control → Build → Runtime → Gateway → demo-app
one shared W3C trace_id spans all five services (Tempo)
GET /v1/logs?trace_id=… → correlated entries across services
forge logs --follow → live lines for the deployment
induce Gateway 5xx → HighErrorRate visible in GET /v1/alerts
```

```text
run.sh (minted traceparent)
   ├── Control /v1/projects
   ├── Build   /v1/builds
   ├── Runtime /v1/node
   └── Gateway → demo-app   ──► Tempo (one trace)
                                Loki  (correlated logs via Observe)
                                Prometheus rules → Observe /v1/alerts
```

## What this demo checks

* A single request path produces **one** Tempo trace with spans from
  `control`, `build`, `runtime`, `gateway`, and `demo-app`.
* Observe `GET /v1/logs?trace_id=…` returns entries from multiple services;
  the same logs are also queryable by `project` + `deployment`.
* `forge logs --follow` tails live lines for the demo deployment.
* Stopping the upstream and flooding Gateway 5xx traffic fires
  `HighErrorRate` (visible in `GET /v1/alerts`).
* The demo app satisfies the epic-01 runtime contract (`/`, `/health/*`).
* No secret-looking material appears in the telemetry dumps used for assertions.
* Compose services started by the demo are stopped on exit.

**Auth:** this gate runs with `FORGE_AUTH_MODE=dev` for simplicity. Enforced
Identity-bound Observe queries are covered by step `12.04` tests.

**Alert tuning:** the overlay mounts
`demos/12-observability/prometheus/forge-alerts.yml` with
`HighErrorRate` `for: 10s` and a `1m` rate window so the gate finishes quickly.
Defaults in `deploy/observability/prometheus/rules/` remain unchanged for
normal `make dev`.

## Run

From the repository root:

```bash
make demo DEMO=12
```

Expect a final `demo 12 PASSED` line and exit code `0`:

```text
[trace] single trace spans: control, build, runtime, gateway, demo-app OK
[logs] correlated logs across 4 services OK
[tail] forge logs --follow streamed lines OK
[alert] HighErrorRate fired on induced errors OK
demo 12 PASSED
```

Optional phase flags (CI targeting):

```bash
./demos/12-observability/run.sh all
./demos/12-observability/run.sh trace
./demos/12-observability/run.sh logs
./demos/12-observability/run.sh alert
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_AUTH_MODE` | `dev` | Demo gate auth mode |
| `FORGE_OBSERVE_URL` | `http://127.0.0.1:4106` | Observe API |
| `FORGE_TEMPO_URL` | `http://127.0.0.1:3002` | Tempo query (host port) |
| `FORGE_LOKI_URL` | `http://127.0.0.1:3003` | Loki push/query |
| `FORGE_ALERT_ERROR_RATE_THRESHOLD` | `0.05` | Documented knob (rules overlay is source of truth) |
| `FORGE_ALERT_ERROR_RATE_FOR` | `10s` | Documented knob (rules overlay `for:`) |
| `DEMO_IMAGE` | `localhost:5000/demo-observability:12` | Pre-built demo app image |

## Notes

* The demo app exports OTLP traces to `host.docker.internal:4317` (collector
  host port) because Runtime workloads are not attached to the Compose network.
* Structured stdout logs from platform services are shipped into Loki by
  `run.sh` for the shared `trace_id` so Observe correlation queries succeed
  without requiring every service to push OTLP logs yet.
