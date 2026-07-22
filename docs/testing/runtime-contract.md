# Runtime contract validation

The formal contract is documented in [`docs/contracts/runtime-contract.md`](../contracts/runtime-contract.md).

## Shared validator (step 01.02)

Entrypoint: [`tools/contract-validator`](../../tools/contract-validator/README.md).

```bash
./tools/contract-validator/run.sh --help

./tools/contract-validator/run.sh \
  --base-url http://127.0.0.1:8099 \
  --expect-service fixture \
  --expect-language go

make contract-validate \
  BASE_URL=http://127.0.0.1:4201 \
  EXPECT_SERVICE=demo-go-api \
  EXPECT_LANGUAGE=go
```

### What it checks

* Process listens on the provided base URL
* `GET /health/live` → `200`
* `GET /health/ready` → `200`
* `GET /` → `200` JSON with `service`, `language`, `status`
* Optional log file: each line matches the epic 01 required fields in `runtime-log.schema.json`
* Optional graceful shutdown via `--shutdown-pid` or `--shutdown-container` within the grace window (default 10s)

Exit `0` on pass; non-zero on failure with actionable stderr.

### Tests

```bash
./tools/contract-validator/test_validator.sh
# or
make test-unit
# discoverable wrapper:
./tests/contracts/test_runtime_contract_validator.sh
```

Tests use an in-tree fixture HTTP server (`fixture_server.py`) so they do not depend on unfinished demo apps.

### Language demos (upcoming)

Steps `01.03`–`01.07` will start each demo on ports `4201`–`4205` and invoke the same runner. `make demo DEMO=01` will exercise all five languages once those apps exist.
