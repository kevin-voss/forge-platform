# Alerting (local platform)

Step `12.06` provisions basic Prometheus alert rules, Alertmanager delivery to a
dev-only webhook sink, Grafana alert-state panels, and Observe
`GET /v1/alerts`.

## Components

| Piece | Location |
|---|---|
| Alert rules | `deploy/observability/prometheus/rules/forge-alerts.yml` |
| Rule unit tests | `deploy/observability/prometheus/rule-tests/forge-alerts_test.yml` |
| Alertmanager | Compose `alertmanager` (host `:3004`) |
| Webhook sink | Compose `alert-webhook-sink` (internal only) |
| Observe API | `GET /v1/alerts` on `:4106` |

## Rules

| Alert | Expression (summary) | Default `for` |
|---|---|---|
| `ServiceDown` | `forge_service_up == 0` | `30s` |
| `HighErrorRate` | 5xx / total request rate > `0.05` | `60s` |
| `NodeOffline` (optional) | `forge_node_heartbeat_age_seconds > 90` | `30s` |

Rules evaluate the 12.02 metric contract (`forge_service_up`,
`forge_http_requests_total{http_status_class="5xx"}`, node heartbeat age).

### Tuning thresholds

Defaults match the Observe env knobs (documented for operators; the provisioned
rule file is the runtime source of truth):

| Knob | Default | Rule field to edit |
|---|---|---|
| `FORGE_ALERT_SERVICE_DOWN_FOR` | `30s` | `ServiceDown` `for:` |
| `FORGE_ALERT_ERROR_RATE_THRESHOLD` | `0.05` | `HighErrorRate` expr `> 0.05` |
| `FORGE_ALERT_ERROR_RATE_FOR` | `60s` | `HighErrorRate` `for:` |

After editing `forge-alerts.yml`, reload Prometheus:

```bash
curl -X POST http://127.0.0.1:3001/-/reload
# or: docker compose restart prometheus
```

`promtool check rules` / `promtool test rules` (via
`make -C services/forge-observe test-unit`) fail the build on syntax errors.

### Absent metrics

`ServiceDown` fires when a series exists with value `0`. A service that has
never been scraped does not emit `forge_service_up`, so it will not appear in
`ServiceDown` until it has reported at least once. Prefer readiness/deploy
checks for “never came up” cases.

## Delivery sink (dev only)

Alertmanager routes every alert to `alert-webhook-sink`, which prints JSON lines
for firing/resolved webhook payloads. This is **not** a production pager.

```bash
docker compose logs -f alert-webhook-sink
```

## Observe status API

```bash
curl -s http://127.0.0.1:4106/v1/alerts | jq .
```

Response items: `{ name, state: firing|pending, labels, since, value }` with a
bounded label set. When Alertmanager is unreachable the endpoint returns `503`
(`alerting_unavailable`); Grafana dashboards still render from Prometheus.

## Grafana

The **Forge Platform** dashboard includes:

* **Firing alerts** — count of firing `ServiceDown` / `HighErrorRate` / `NodeOffline`
* **Alert state** — table of `ALERTS{...}` series

## Manual verification

```bash
make dev
# stop a workload that exports forge_service_up == 0, wait >30s
curl -s localhost:4106/v1/alerts | jq '.[] | select(.name=="ServiceDown") | {state, labels}'
docker compose logs alert-webhook-sink | tail
```
