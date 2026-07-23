# Grafana dashboard catalog

Predefined Forge dashboards are provisioned as code and load automatically when
Grafana starts (Compose service `grafana` on port `3000`).

## Location

| Path | Role |
|---|---|
| `deploy/observability/grafana/dashboards/*.json` | Dashboard definitions |
| `deploy/observability/grafana/provisioning/dashboards/forge.yaml` | Grafana file provider (source of truth) |
| `infrastructure/grafana/provisioning/dashboards/forge.yaml` | Same provider, loaded via Compose provisioning mount |
| `infrastructure/grafana/provisioning/datasources/datasources.yml` | Prometheus / Loki / Tempo UIDs |

Compose mounts the dashboards at `FORGE_GRAFANA_DASHBOARD_DIR`
(`/etc/grafana/forge-dashboards`) and the provider into
`GF_PATHS_PROVISIONING` (`/etc/grafana/provisioning`).

## Catalog

| UID | Title | Purpose | Template variables |
|---|---|---|---|
| `forge-platform` | Forge Platform | Fleet-wide service up/down, request rate, error rate, p95 latency, firing alert count/state | — |
| `forge-service` | Forge Service | Per-service throughput, error %, latency quantiles, log volume | `service` |
| `forge-deployment` | Forge Deployment | Rollout steps/transitions, ready replicas, per-deployment errors | `forge.deployment` |
| `forge-runtime` | Forge Runtime | Node count, free slots, heartbeat age, offline rate | `forge.node` |

## Datasources

Stable UIDs (must match panel JSON):

* `prometheus` — Prometheus
* `loki` — Loki
* `tempo` — Tempo

## Metric contract

Panels query the standard metrics from step `12.02` / Control deployment and
node metrics, including:

* `forge_service_up`
* `forge_http_requests_total`
* `forge_http_request_duration_seconds` (+ `_bucket` for histograms)
* `forge_replicas_ready` (exported as `forge_replicas_ready_total` when recorded as a counter)
* `forge_rollout_step_total`, `forge_rollout_result_total`, `forge_deployment_transitions_total`
* `forge_nodes_total`, `forge_node_free_slots`, `forge_node_heartbeat_age_seconds`, `forge_node_offline_total`

Missing series degrade to Grafana “No data”; they do not break dashboard load.

## Manual check

```bash
make dev
curl -s -u admin:admin 'localhost:3000/api/search?query=Forge' | jq '.[].title'
curl -s -u admin:admin localhost:3000/api/dashboards/uid/forge-platform | jq '.dashboard.title'
```

Automated: `make -C services/forge-observe test-dashboards` (plus unit parity tests
under `internal/dashboards`).
