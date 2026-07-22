# Runtime contract validation

The formal contract is documented in [`docs/contracts/runtime-contract.md`](../contracts/runtime-contract.md).

## Current status (step 01.01)

Documentation and schemas only. Manual checks:

```bash
test -f docs/contracts/runtime-contract.md
test -f contracts/openapi/runtime-contract.openapi.yaml
python3 -c "import json; json.load(open('contracts/examples/runtime-info-response.json'))"
python3 -c "import json; json.load(open('contracts/examples/runtime-log-line.json'))"
python3 -c "import json; json.load(open('contracts/examples/runtime-log.schema.json'))"
```

## Upcoming (step 01.02)

`tools/contract-validator` will automate:

* `GET /health/live` and `GET /health/ready` return success
* `GET /` returns the required identity fields
* stdout log lines validate against `runtime-log.schema.json`
* graceful shutdown exits within the documented grace period

Demo apps (`01.03`–`01.07`) will be checked with that runner via `make demo DEMO=01`.
