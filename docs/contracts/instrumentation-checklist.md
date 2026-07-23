# Instrumentation checklist

Normative per-service requirements for OpenTelemetry instrumentation on Forge
platform services. Correlation field names and headers are defined in
[observability-correlation.md](observability-correlation.md).

This checklist is the acceptance bar for step `12.02`. Control, Runtime,
Gateway, and Build MUST satisfy every item below. Later services SHOULD follow
the same checklist when they land.

## Required environment

| Variable | Role | Default |
|---|---|---|
| `FORGE_OTEL_ENABLED` | Master switch; `false` uses no-op SDK | `true` |
| `FORGE_OTEL_EXPORTER_ENDPOINT` | OTLP collector base URL | `http://otel-collector:4317` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Alias accepted when `FORGE_OTEL_*` unset | same |
| `FORGE_SERVICE_NAME` | Resource `service.name` | service-specific |
| `FORGE_ENV` | Resource `deployment.environment` | `development` |

## Checklist (every service)

### Bootstrap

- [ ] Initialize the OpenTelemetry SDK at process start (traces + metrics).
- [ ] Export via OTLP to the collector endpoint above (gRPC `4317` preferred).
- [ ] Attach resource attributes: `service.name`, `deployment.environment`.
- [ ] Attach `forge.service` (platform service name) always; attach
      `forge.project` / `forge.deployment` / `forge.node` when known.
- [ ] Telemetry export is **fail-open**: collector down must not crash, hang, or
      return 5xx for application traffic.

### Trace context propagation

- [ ] Extract inbound W3C `traceparent` when present and valid.
- [ ] On missing or malformed `traceparent`, start a **new root trace**.
- [ ] Create a server span for each inbound HTTP request (skip or sample lightly
      only for `/health/*` if needed; still keep request metrics healthy).
- [ ] Inject active `traceparent` on every outbound HTTP call to other Forge
      services.
- [ ] Gateway (edge): mint `X-Forge-Request-ID` when absent; propagate on
      responses and upstreams. Legacy `X-Request-Id` MAY be dual-written with the
      same value for older clients.

### Structured logs

JSON logs MUST include when available (snake_case per correlation contract):

| Field | Required when |
|---|---|
| `trace_id` | Active valid span |
| `span_id` | Active valid span |
| `request_id` | Request context established |
| `forge.service` | Always (or process default) |
| `forge.project` | Known |
| `forge.deployment` | Known |
| `forge.node` | Known (Runtime) |

Legacy camelCase `requestId` / `traceId` / `spanId` MAY also be emitted for
backward compatibility, but snake_case is normative for Observe queries.

### Standard metrics

Emit (names are normative):

| Metric | Type | Labels (bounded only) |
|---|---|---|
| `forge_service_up` | gauge | none (or `service`) |
| `forge_http_requests_total` | counter | `http_method`, `http_status_class` (or status code) |
| `forge_http_request_duration_seconds` | histogram | `http_method`, `http_status_class` |

Additional service-specific metrics are allowed. **Do not** put `request_id`,
`trace_id`, paths with unbounded cardinality, or free-text in metric labels.

### Privacy

- [ ] Never attach secrets, tokens, session cookies, or PII to spans, metric
      labels, or correlation log fields.
- [ ] Prefer opaque Control/Identity ids for `forge.*` attributes.

## Verification

See [docs/testing/instrumentation.md](../testing/instrumentation.md).

## Services covered by 12.02

| Service | Language | Bootstrap package |
|---|---|---|
| forge-control | Kotlin | `forge.control.observability` |
| forge-runtime | Rust | `observability` module |
| forge-gateway | Go | `internal/observability` |
| forge-build | Go | `internal/observability` |
