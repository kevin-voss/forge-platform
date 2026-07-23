# Instrumentation verification

How to verify that Control, Runtime, Gateway, and Build satisfy
[`docs/contracts/instrumentation-checklist.md`](../contracts/instrumentation-checklist.md).

## Automated checks

Run unit tests in each instrumented service (no collector required):

```bash
# Gateway
(cd services/forge-gateway && go test ./internal/observability/... ./internal/middleware/...)

# Build
(cd services/forge-build && go test ./internal/observability/...)

# Control
(cd services/forge-control && ./gradlew test --tests '*Observability*' --tests '*Telemetry*')

# Runtime
(cd services/forge-runtime && cargo test observability)
```

These cover:

* inbound `traceparent` extract + outbound inject
* missing/malformed `traceparent` → new root trace
* log enricher fields (`trace_id`, `span_id`, `request_id`)
* metric label cardinality lint (no `request_id` / `trace_id` labels)
* fail-open init against an unreachable exporter endpoint

## Manual / integration (collector up)

```bash
make dev
# Edge request through Gateway; capture Forge request id
RID=$(curl -s -D - -H 'Host: demo.localhost' localhost:4000/ -o /dev/null \
  | awk -F': ' 'tolower($1)=="x-forge-request-id"{print $2}' | tr -d '\r')
echo "request id: $RID"

# Tempo should show traces tagged with forge.service (may take a few seconds)
curl -s "localhost:3200/api/search?tags=forge.service" | jq '.traces | length'

# Fail-open: stop collector; Gateway/Control still serve
docker stop otel-collector; sleep 3
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: demo.localhost' localhost:4000/
docker start otel-collector
```

Expect HTTP `200` (or the normal upstream status) while the collector is down —
never a crash or process exit.

## Contract field names

Shared constants live in `services/forge-observe/internal/correlation`.
Instrumented services MUST use the same header and log field strings as
[`docs/contracts/observability-correlation.md`](../contracts/observability-correlation.md).
