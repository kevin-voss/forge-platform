# Observability correlation contract

Normative correlation model for Forge Platform telemetry. Every service that
emits logs, metrics, or traces — and every query surface in Forge Observe —
MUST use these headers, resource attributes, and log field names.

Machine-readable constants (Go):
`services/forge-observe/internal/correlation/model.go`

Companion OpenAPI (Observe skeleton):
[`contracts/openapi/forge-observe.openapi.yaml`](../../contracts/openapi/forge-observe.openapi.yaml)

## Headers

| Header | Normative name | Role |
|---|---|---|
| W3C Trace Context | `traceparent` | Propagate trace/span IDs across HTTP hops |
| Forge request ID | `X-Forge-Request-ID` | Stable edge request identifier (independent of trace) |

Rules:

1. **Inbound:** extract `traceparent` when present and valid; otherwise start a
   new root trace at the edge (Gateway) or when acting as a root producer.
2. **Outbound:** inject the active `traceparent` on every outbound HTTP call.
3. **Request ID:** if inbound `X-Forge-Request-ID` is absent, Gateway (edge)
   MUST mint one; other services MUST propagate the value they received.
4. Do not put secrets, tokens, or PII in either header value.

## Resource attributes

Attach these OpenTelemetry resource (or span) attributes when known:

| Attribute | Meaning |
|---|---|
| `forge.project` | Project id / slug |
| `forge.deployment` | Deployment id |
| `forge.service` | Service name (platform or workload) |
| `forge.node` | Runtime node id when applicable |

Cardinality: keep these labels bounded (ids/slugs). Do **not** put
`request_id` or free-text on Prometheus metric labels — use logs/traces for
high-cardinality correlation.

## Structured log fields

JSON logs SHOULD include (when available):

| Field | Source |
|---|---|
| `trace_id` | Active W3C trace id (hex) |
| `span_id` | Active span id (hex) |
| `request_id` | Value of `X-Forge-Request-ID` |
| `forge.project` | Same as resource attribute |
| `forge.deployment` | Same as resource attribute |
| `forge.service` | Same as resource attribute |
| `forge.node` | Same as resource attribute |

Base runtime-contract fields (`timestamp`, `level`, `service`, `message`) still
apply; see [runtime-contract.md](runtime-contract.md).

## Privacy and safety

Correlation attributes MUST NOT include:

* raw request/response payloads
* API tokens, session cookies, or secret values
* end-user PII (emails, names, addresses) as correlation keys

Prefer opaque ids already used by Control/Identity.

## Degraded mode (Observe backends)

Forge Observe talks to Loki (logs), Tempo (traces), and Prometheus (metrics)
with **read-only** clients and per-request timeouts
(`FORGE_BACKEND_TIMEOUT_MS`, default 2000ms). Clients never hang indefinitely.

| Backend down | `/health/ready` (default requires all) | Still works |
|---|---|---|
| Loki | 503 | Identity, live, Tempo/Prometheus status in `/v1/health/backends` |
| Tempo | 503 | Identity, live, Loki/Prometheus status |
| Prometheus | 503 | Identity, live, Loki/Tempo status |

Narrow the readiness gate with
`FORGE_OBSERVE_READY_REQUIRE_BACKENDS` (comma list: `loki`, `tempo`,
`prometheus`) when a local profile intentionally omits a backend.
`GET /v1/health/backends` always returns `{ loki, tempo, prometheus }` with
`ok` or `down` so operators can see which dependency failed.

Log query (12.04) and CLI tail (12.05) depend on Loki; dashboards (12.03) and
alerts (12.06) depend on Prometheus/Grafana; distributed-trace demos depend on
Tempo. When a backend is down, those features degrade; identity and liveness do
not.

## Consumers of this contract

* Instrumentation checklist (step 12.02) on Control / Runtime / Gateway / Build
* Grafana dashboards and log query filters (12.03–12.04)
* `forge logs --follow` (12.05)
