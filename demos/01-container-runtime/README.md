# Demo 01: Container runtime (Go + Kotlin + Python)

Language slices of the Forge runtime contract: production-shaped workloads that
listen on `PORT`, expose health + identity, emit structured JSON logs, and shut
down cleanly on `SIGTERM`.

This step covers **Go** (port `4201`), **Kotlin** (port `4202`), and **Python**
(port `4204`). Later steps add Rust and Elixir on ports `4203` and `4205`.

## What this demo checks

* Go, Kotlin, and Python images build via Compose
* `GET /health/live` and `GET /health/ready` → `200`
* `GET /` identity JSON includes the expected `"language"`
* Structured stdout logs match the epic 01 required fields
* `docker stop` (SIGTERM) exits within the 10s grace window
* Shared [`tools/contract-validator`](../../tools/contract-validator/README.md) passes for each language

## Ports

| Service | Host | Container |
|---|---:|---:|
| `demo-go-api` | 4201 | 8080 (`PORT`) |
| `demo-kotlin-api` | 4202 | 8080 (`PORT`) |
| `demo-python-api` | 4204 | 8080 (`PORT`) |

See [`docs/operations/ports.md`](../../docs/operations/ports.md).

## Configuration

In-container defaults (Compose / Dockerfile / `.env.example`):

```text
PORT=8080
FORGE_SERVICE_NAME=demo-<lang>-api
FORGE_SERVICE_VERSION=0.1.0
FORGE_LOG_LEVEL=info
FORGE_ENV=development
```

Host publish pattern: `4201:8080` (Go), `4202:8080` (Kotlin), `4204:8080` (Python).

## Run

```bash
make demo DEMO=01
```

Or directly:

```bash
./demos/01-container-runtime/run.sh
```

## Local unit tests

```bash
cd demos/01-container-runtime/apps/go
go test ./...

cd demos/01-container-runtime/apps/kotlin
./gradlew test

cd demos/01-container-runtime/apps/python
python -m unittest -v test_server.py
```

## Layout

```text
demos/01-container-runtime/
├── README.md
├── run.sh
├── compose.yaml
└── apps/
    ├── go/       # demo-go-api (no platform SDK imports)
    ├── kotlin/   # demo-kotlin-api (Ktor/Netty; no platform SDK)
    └── python/   # demo-python-api (stdlib only; no platform SDK)
```
