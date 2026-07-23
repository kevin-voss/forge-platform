# Epic 12: Forge Observe

## Status

In progress

## Goal

Stand up Forge Observe — a Go service on port `4106` — that turns the foundation OTEL/Prometheus/Tempo/Loki/Grafana stack into a unified logs/metrics/traces experience for Forge. When this epic is done, every completed platform service is instrumented with trace propagation and structured logs, Grafana ships provisioned dashboards for platform/service/deployment/runtime, logs can be queried and filtered by project/deployment/request/trace ID, `forge logs --follow` tails a service, basic alerts fire on service-down/error-rate, and a single request can be followed across services as one distributed trace. Proven by `demos/12-observability`.

## Why this epic exists

By this point Forge runs many polyglot services; debugging requires following one request across all of them and querying logs by business dimensions (project/deployment), not just container names. `specs.md` Step 12 defines unified observability on top of the `00` OTEL stack: trace propagation, correlation IDs, dashboards, log query, CLI tail, and alerts. Later epics (Agents 15, Workflows 16) rely on this to diagnose incidents.

## Primary code areas

* `services/forge-observe/` — new Go service (correlation/log-query API, alert evaluation, dashboard provisioning glue)
* `services/forge-control`, `forge-runtime`, `forge-gateway`, `forge-build` — instrumentation checklist applied (trace context propagation, structured logs, metrics)
* `tools/forge-cli/` — `forge logs --follow` (`12.05`)
* `deploy/observability/` (or `00` stack config) — Grafana dashboards + alert rules provisioning
* `demos/12-observability/` — single distributed trace across the deploy path

## Suggested language

Go for the Observe service (per `specs.md` Step 12). Instrumentation uses each service's native OTEL SDK. Backends (Prometheus/Tempo/Loki/Grafana) come from foundation `00`.

## Spec references

* `specs.md` → Step 12: Forge Observe (OTEL ingestion, trace propagation, structured log collection, platform/service/deployment/runtime dashboards, basic alerts, CLI log tail, correlation by request/trace IDs)
* `specs.md` → Step 00 (OTEL/Grafana stack)
* `specs.md` → Step 06 demo path (instrumented flow through Build/Runtime/Gateway)
* `docs/implementation/MASTER_PLAN.md` → Epic 12 catalog + port `4106`

## Dependencies

* Foundation `00` — OpenTelemetry collector, Prometheus, Tempo, Loki, Grafana under Compose
* Epics `02-forge-control`, `04-forge-runtime`, `05-forge-gateway`, `06-forge-build` — the services to instrument and the deploy path traced in the demo
* Epic `01-runtime-contract` — structured-log conventions to build on

## Out of scope for this epic

* Replacing the OTEL/Grafana backends with a custom store
* Long-term retention/cost management of telemetry
* SLO/error-budget tooling beyond basic alert rules
* Per-user dashboards / multi-tenant Grafana orgs (single Grafana org for local)
* Instrumenting services not yet built (they instrument as they land, following the checklist)

## Success demo

```bash
make demo DEMO=12
```

```text
CLI → Control → Build → Runtime → Gateway → demo application
One trace ID spans the whole request path (visible in Tempo/Grafana)
Logs for the demo deployment queryable by project + deployment + trace ID
forge logs --follow tails the demo service live
An induced error fires the error-rate alert
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [12.01](../steps/12-forge-observe/12.01-skeleton-and-correlation-api-design.md) | Skeleton + correlation API design | Complete | Go service on 4106; correlation model |
| [12.02](../steps/12-forge-observe/12.02-instrumentation-checklist.md) | Instrumentation checklist on Control/Runtime/Gateway/Build | Complete | Trace propagation + structured logs + metrics |
| [12.03](../steps/12-forge-observe/12.03-grafana-dashboards.md) | Grafana dashboards (platform/service/deployment/runtime) | Complete | Provisioned dashboards |
| [12.04](../steps/12-forge-observe/12.04-log-query-and-filter.md) | Log query/filter by project/deployment/request/trace ID | Complete | Correlated log search API |
| [12.05](../steps/12-forge-observe/12.05-cli-logs-follow.md) | CLI `forge logs --follow` | Not started | Live tail via Observe/Runtime |
| [12.06](../steps/12-forge-observe/12.06-basic-alert-rules.md) | Basic alert rules | Not started | Service down / error rate |
| [12.07](../steps/12-forge-observe/12.07-demo-12-observability.md) | Demo `12-observability` (one distributed trace) + gate | Not started | Single trace end-to-end; epic gate |

## Assumptions

* The foundation `00` stack provides OTEL Collector + Prometheus + Tempo + Loki + Grafana; Observe integrates with them rather than replacing them. Observe's own APIs (log query, correlation, alert status) are thin query layers over Loki/Tempo/Prometheus plus Forge-specific correlation metadata.
* Correlation is by W3C `traceparent` (trace ID) propagated across services, plus a Forge `X-Forge-Request-ID` and resource attributes `forge.project`, `forge.deployment`, `forge.service`, `forge.node` attached to spans + logs.
* Instrumentation follows a shared checklist per service; each service uses its native OTEL SDK exporting to the collector. Telemetry export failures must never crash application services (fail-open on telemetry).
* Dashboards + alert rules are provisioned as code (Grafana provisioning files + Prometheus/Loki alert rules) checked into the repo.
* `forge logs --follow` streams from Observe (Loki query + tail) with a fallback to Runtime's log streaming (`04.05`) when Loki is unavailable.

## Open questions

* Does Observe proxy Loki/Tempo queries or expose its own correlation-enriched API on top? Assumption: thin correlation-enriched API over the backends; Grafana still available directly.
* Log tail transport: SSE vs WebSocket vs chunked HTTP. Assumption: SSE for `forge logs --follow` (Gateway supports SSE from `05.06`).
* Metric cardinality of `forge.deployment`/`forge.project` labels. Assumption: bounded labels (project, service, deployment id) documented; avoid unbounded labels like request id in metrics (use logs/traces for those).
* Alerting delivery target for local dev. Assumption: alerts visible in Grafana + a log/webhook sink; no external pager in local.

## Next step to implement

**[12.05](../steps/12-forge-observe/12.05-cli-logs-follow.md) — CLI `forge logs --follow`**.
