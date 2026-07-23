# Contracts

Machine-readable Forge Platform contracts.

## OpenAPI

| Contract | Path |
|---|---|
| Runtime (deployable app) | [`openapi/runtime-contract.openapi.yaml`](openapi/runtime-contract.openapi.yaml) |
| Forge Control | [`openapi/forge-control.openapi.yaml`](openapi/forge-control.openapi.yaml) |
| Forge Build | [`openapi/forge-build.openapi.yaml`](openapi/forge-build.openapi.yaml) |
| Forge Events | [`openapi/forge-events.openapi.yaml`](openapi/forge-events.openapi.yaml) |

## Examples & schemas

| Artifact | Path |
|---|---|
| Runtime log JSON Schema | [`examples/runtime-log.schema.json`](examples/runtime-log.schema.json) |
| Platform event JSON Schemas | [`events/`](events/) (`application.crashed`, `build.*`, `deployment.*`, …) |
| Platform event examples | [`examples/events/`](examples/events/) |
| `forge.yaml` JSON Schema | [`examples/forge.schema.json`](examples/forge.schema.json) |
| `forge.yaml` example | [`examples/forge.yaml.example`](examples/forge.yaml.example) |
| Build create request/response | [`examples/forge-build-create-request.json`](examples/forge-build-create-request.json), [`examples/forge-build-create-response.json`](examples/forge-build-create-response.json) |
| Build status / error fixtures | [`examples/forge-build-status-response.json`](examples/forge-build-status-response.json), [`examples/forge-build-error-response.json`](examples/forge-build-error-response.json) |

Human-oriented notes live in [`docs/contracts/`](../docs/contracts/).
