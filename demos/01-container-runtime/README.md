# Demo 01: Container runtime (Go)

First language slice of the Forge runtime contract: a production-shaped Go
workload that listens on `PORT`, exposes health + identity, emits structured
JSON logs, and shuts down cleanly on `SIGTERM`.

Later steps add Python, Kotlin, Rust, and Elixir on ports `4202`–`4205`.

## What this demo checks

* Go image builds via Compose
* `GET /health/live` and `GET /health/ready` → `200`
* `GET /` identity JSON includes `"language":"go"`
* Structured stdout logs match the epic 01 required fields
* `docker stop` (SIGTERM) exits within the 10s grace window
* Shared [`tools/contract-validator`](../../tools/contract-validator/README.md) passes

## Ports

| Service | Host | Container |
|---|---:|---:|
| `demo-go-api` | 4201 | 8080 (`PORT`) |

See [`docs/operations/ports.md`](../../docs/operations/ports.md).

## Configuration

In-container defaults (Compose / Dockerfile / `.env.example`):

```text
PORT=8080
FORGE_SERVICE_NAME=demo-go-api
FORGE_SERVICE_VERSION=0.1.0
FORGE_LOG_LEVEL=info
FORGE_ENV=development
```

Host publish pattern: `4201:8080`.

## Run

```bash
make demo DEMO=01
```

Or directly:

```bash
./demos/01-container-runtime/run.sh
```

## Local unit tests (Go)

```bash
cd demos/01-container-runtime/apps/go
go test ./...
```

## Layout

```text
demos/01-container-runtime/
├── README.md
├── run.sh
├── compose.yaml
└── apps/go/          # demo-go-api (no platform SDK imports)
```
