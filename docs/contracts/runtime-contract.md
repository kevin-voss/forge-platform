# Runtime contract

Normative contract for every Forge deployable: product containers and (later) platform services. This document is the source of truth for epic `01-runtime-contract`. Demo apps and the shared validator (`tools/contract-validator`, step `01.02`) must follow it.

Machine-readable companions:

* OpenAPI: [`contracts/openapi/runtime-contract.openapi.yaml`](../../contracts/openapi/runtime-contract.openapi.yaml)
* Log JSON Schema: [`contracts/examples/runtime-log.schema.json`](../../contracts/examples/runtime-log.schema.json)
* Examples: [`contracts/examples/`](../../contracts/examples/)

Spec context: `specs.md` §2.2, §2.3, §5.4–5.5, Step 01. Where this document and the epic steps are more specific than `specs.md`, **this contract wins for epic 01**.

## Runtime boundary

Every deployable:

1. ships as an OCI-compatible container image
2. listens on `PORT` (process binds `0.0.0.0:PORT`)
3. exposes liveness and readiness HTTP endpoints
4. exposes a JSON identity response at `GET /`
5. logs structured JSON lines to stdout
6. receives configuration from environment variables
7. handles graceful shutdown on `SIGTERM`
8. publishes OpenTelemetry signals where supported (**recommended**, not required for epic 01 demos)

Containers are the unit of deployment. Language-specific frameworks are free choices inside the image; the host and platform only rely on this contract.

## HTTP surface

| Method | Path | Requirement |
|---|---|---|
| `GET` | `/health/live` | **Required.** `200` when the process is alive. |
| `GET` | `/health/ready` | **Required.** `200` when ready to receive traffic; non-`200` when not ready. |
| `GET` | `/` | **Required.** `200` JSON identity payload. |
| `GET` | `/metrics` | **Recommended only.** Not required for epic 01 demos or the shared validator. |

Health and identity endpoints are unauthenticated by design for local demos. TLS is not required in epic 01.

### Identity response (`GET /`)

Minimum fields:

```json
{
  "service": "demo-go-api",
  "language": "go",
  "status": "running"
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `service` | string | yes | Logical service name (usually `FORGE_SERVICE_NAME`) |
| `language` | string | yes | Implementation language: `go`, `kotlin`, `rust`, `python`, `elixir`, … |
| `status` | string | yes | At least `"running"` when serving traffic |
| `version` | string | no | Recommended; usually `FORGE_SERVICE_VERSION` |
| `uptime_seconds` | number | no | Recommended |

Canonical example: [`contracts/examples/runtime-info-response.json`](../../contracts/examples/runtime-info-response.json).

## Configuration

### Normative for epic 01 workloads / demos

```text
PORT                 # listen port (required; 12-factor style)
FORGE_SERVICE_NAME   # service identity in logs and GET /
FORGE_SERVICE_VERSION
FORGE_LOG_LEVEL      # debug | info | warn | error
FORGE_ENV            # e.g. development (optional for demos, documented)
```

Example env file: [`contracts/examples/runtime-env.example`](../../contracts/examples/runtime-env.example).

### Platform services (later)

Platform services use the fuller `FORGE_*` set from `specs.md` §5.5 (`FORGE_HTTP_PORT`, `FORGE_DATABASE_URL`, `FORGE_EVENTS_URL`, `FORGE_OTEL_ENDPOINT`, …). Epic 01 demos need only the subset above.

### Decision: `PORT` vs `FORGE_HTTP_PORT`

If both `PORT` and `FORGE_HTTP_PORT` are set, **`PORT` wins** for the listen address. Workloads and demos should prefer `PORT`. Platform services may set `FORGE_HTTP_PORT` for consistency with §5.5, but binding still follows `PORT` when present.

### Startup failures

* Missing or non-integer `PORT` → process exits non-zero at startup (do not bind a default silently in demos).
* Invalid `FORGE_LOG_LEVEL` → treat as error at startup or coerce to `info` and log a warning; demos should prefer fail-fast.

## Structured logs

All deployables must emit **one JSON object per line** on stdout.

### Required fields (epic 01 demos)

```json
{
  "timestamp": "2026-07-22T14:30:00Z",
  "level": "info",
  "service": "demo-go-api",
  "message": "listening"
}
```

| Field | Required | Notes |
|---|---|---|
| `timestamp` | yes | RFC 3339 / ISO-8601 UTC |
| `level` | yes | `debug` \| `info` \| `warn` \| `error` |
| `service` | yes | Same logical name as identity `service` |
| `message` | yes | Human-readable event summary |
| `version` | no | Recommended |
| `request_id` | no | Recommended when handling HTTP |
| `trace_id` | no | Recommended when tracing is enabled |

Schema: [`contracts/examples/runtime-log.schema.json`](../../contracts/examples/runtime-log.schema.json).  
Example line: [`contracts/examples/runtime-log-line.json`](../../contracts/examples/runtime-log-line.json).

Platform services should eventually satisfy the fuller §5.4 field set. Epic 01 demos are validated against the required subset above.

## Graceful shutdown

On `SIGTERM` (and equivalently on Compose/Docker stop):

1. stop accepting new work
2. finish or drain in-flight requests where practical
3. exit `0` within the grace period

**Default grace for epic 01 demos: 10 seconds.** If the process has not exited by then, the runtime may force-kill; demos must not rely on a longer window.

## Failure handling

| Condition | Expected behavior |
|---|---|
| Process alive but not ready | `GET /health/ready` returns non-`200` |
| Process dead / not listening | liveness probe fails |
| Invalid `PORT` | non-zero exit at startup |
| After `SIGTERM` | no new work; clean exit within grace |

For epic 01 positive demos, ready may equal live. Negative “not ready” fixtures are reserved for the contract validator (step `01.02`) if needed.

## Observability

* Structured JSON logs to stdout: **required**
* OpenTelemetry export: **recommended / deferred** for epic 01 demos — mention endpoints and conventions, do not require instrumentation in demo apps
* `GET /metrics`: **recommended only**, not part of the shared validator’s hard checks in epic 01

## Anti-goals

* No platform SDKs under `packages/*` in demo apps
* No Forge Control / Runtime / Gateway / Identity dependencies for epic 01 demos
* Do not treat `/metrics` or OTEL as hard acceptance criteria for epic 01
* Do not introduce language-shared business libraries; share this contract instead (`specs.md` §2.3)

## Validation strategy

Automated compliance is provided by [`tools/contract-validator`](../../tools/contract-validator/README.md) (step `01.02`). It checks listen/health/identity over HTTP, optional JSONL log schema conformance, and optional graceful shutdown (`SIGTERM` or `docker stop`) within the **10s** grace window.

```bash
./tools/contract-validator/run.sh \
  --base-url http://127.0.0.1:4201 \
  --expect-service demo-go-api \
  --expect-language go \
  --log-file /tmp/demo-go.jsonl

# or
make contract-validate BASE_URL=http://127.0.0.1:4201 \
  EXPECT_SERVICE=demo-go-api EXPECT_LANGUAGE=go
```

Language demos (`01.03`–`01.07`) and `make demo DEMO=01` will invoke this runner so every language is checked the same way. See the tool README for flags, exit codes, and the fixture server used in unit tests.

Host ports for the five demo languages are reserved in [`docs/operations/ports.md`](../operations/ports.md) (`4201`–`4205`).

## Demo 01

End-to-end proof of this contract lives in [`demos/01-container-runtime`](../../demos/01-container-runtime/README.md):

| Service | Host port | Language |
|---|---:|---|
| `demo-go-api` | 4201 | Go |
| `demo-kotlin-api` | 4202 | Kotlin |
| `demo-rust-api` | 4203 | Rust |
| `demo-python-api` | 4204 | Python |
| `demo-elixir-api` | 4205 | Elixir |

```bash
make demo DEMO=01
```

That demo builds all five images, checks health/identity/logs/shutdown via the shared validator, and fails closed if any language fails. Demo apps must not depend on platform SDKs.

## Decisions recorded (epic 01)

| Topic | Decision |
|---|---|
| Identity path | `GET /` (not `/info`) |
| Listen env | `PORT` required; if both set, `PORT` beats `FORGE_HTTP_PORT` |
| Log fields for demos | Required: `timestamp`, `level`, `service`, `message` |
| Shutdown grace | 10s default for demos |
| `/metrics` / OTEL | Recommended, not required for epic 01 demos |
| Auth / TLS | Not required for local demos in epic 01 |
