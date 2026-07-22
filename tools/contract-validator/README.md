# Runtime contract validator

Language-agnostic checker for the Forge [runtime contract](../../docs/contracts/runtime-contract.md).

Used by epic `01` demo apps (`01.03`–`01.07`) so every language is validated the same way. The tool speaks only HTTP and OS signals / Docker — it does not import product SDKs.

## Requirements

* Python 3.10+
* `curl` (for local tests)
* Optional: Docker (only when using `--shutdown-container`)

## Usage

```bash
./tools/contract-validator/run.sh \
  --base-url http://127.0.0.1:4201 \
  --expect-service demo-go-api \
  --expect-language go \
  --log-file /tmp/demo-go.jsonl \
  --shutdown-pid <pid> \
  --shutdown-timeout 10s
```

Make convenience target:

```bash
make contract-validate \
  BASE_URL=http://127.0.0.1:4201 \
  EXPECT_SERVICE=demo-go-api \
  EXPECT_LANGUAGE=go \
  LOG_FILE=/tmp/demo-go.jsonl
```

### Flags

| Flag | Required | Description |
|---|---|---|
| `--base-url` | yes | Base URL of the running workload |
| `--expect-service` | no | Expected `GET /` `service` field |
| `--expect-language` | no | Expected `GET /` `language` field |
| `--log-file` | no | JSONL stdout capture to validate against `runtime-log.schema.json` |
| `--shutdown-pid` | no | Send `SIGTERM` and require exit within grace |
| `--shutdown-container` | no | `docker stop -t <grace>` the named container |
| `--shutdown-timeout` | no | Grace window (default `10s`) |
| `--skip-http` | no | Skip HTTP checks (log/shutdown-only) |

### Exit codes

| Code | Meaning |
|---:|---|
| `0` | All selected checks passed |
| `1` | One or more checks failed |
| `2` | Invalid CLI usage |

## Checks

1. **Listen** — base URL accepts connections (`connection refused` → fail)
2. **`GET /health/live`** → `200`
3. **`GET /health/ready`** → `200`
4. **`GET /`** → `200` JSON with `service`, `language`, `status` (optional expected values)
5. **Logs** (optional) — each non-empty line is JSON with `timestamp`, `level`, `service`, `message`; `level` ∈ `debug|info|warn|error`
6. **Shutdown** (optional) — `SIGTERM` / `docker stop` completes within the grace window

## Workload env (for callers)

Demos and products under test should document / set:

```text
PORT
FORGE_SERVICE_NAME
FORGE_SERVICE_VERSION
FORGE_LOG_LEVEL
```

See [`contracts/examples/runtime-env.example`](../../contracts/examples/runtime-env.example).

## How language demos will invoke this

After `01.03`–`01.07` land, each demo app (and `make demo DEMO=01`) will:

1. Start the container/process on its reserved port (`4201`–`4205`)
2. Capture structured stdout to a log file
3. Run:

```bash
./tools/contract-validator/run.sh \
  --base-url "http://127.0.0.1:${PORT}" \
  --expect-service "${FORGE_SERVICE_NAME}" \
  --expect-language "<go|kotlin|rust|python|elixir>" \
  --log-file "${LOG_FILE}" \
  --shutdown-container "<compose_service_or_id>" \
  --shutdown-timeout 10s
```

## Fixture server (tests only)

```bash
python3 tools/contract-validator/fixture_server.py --port 8099 --mode compliant
./tools/contract-validator/run.sh \
  --base-url http://127.0.0.1:8099 \
  --expect-service fixture \
  --expect-language go
```

Modes: `compliant`, `no_ready`, `missing_language`, `ignore_sigterm`.

## Tests

```bash
./tools/contract-validator/test_validator.sh
# or
make test-unit
```
