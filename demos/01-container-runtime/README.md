# Demo 01: Container runtime (five languages)

Language slices of the Forge runtime contract: production-shaped workloads that
listen on `PORT`, expose health + identity, emit structured JSON logs, and shut
down cleanly on `SIGTERM`.

All five languages are covered: **Go** (`4201`), **Kotlin** (`4202`), **Rust**
(`4203`), **Python** (`4204`), and **Elixir** (`4205`).

```text
Docker Compose (demo 01)
├── demo-go-api      :4201
├── demo-kotlin-api  :4202
├── demo-rust-api    :4203
├── demo-python-api  :4204
└── demo-elixir-api  :4205
         │
         ▼
tools/contract-validator (×5)
```

## What this demo checks

* All five images build via Compose
* Every container starts and `GET /health/live` + `GET /health/ready` → `200`
* `GET /` identity JSON includes the expected `"language"`
* Structured stdout logs match the epic 01 required fields
* `docker stop` (SIGTERM) exits within the 10s grace window (no forced kill)
* Required env vars (`PORT`, `FORGE_*`) are respected
* Shared [`tools/contract-validator`](../../tools/contract-validator/README.md) passes for each language
* Partial startup does not report overall success (`run.sh` fails closed)

## Ports

| Service | Host | Container |
|---|---:|---:|
| `demo-go-api` | 4201 | 8080 (`PORT`) |
| `demo-kotlin-api` | 4202 | 8080 (`PORT`) |
| `demo-rust-api` | 4203 | 8080 (`PORT`) |
| `demo-python-api` | 4204 | 8080 (`PORT`) |
| `demo-elixir-api` | 4205 | 8080 (`PORT`) |

See [`docs/operations/ports.md`](../../docs/operations/ports.md) and
[`docs/contracts/runtime-contract.md`](../../docs/contracts/runtime-contract.md).

## Configuration

In-container defaults (Compose / Dockerfile):

```text
PORT=8080
FORGE_SERVICE_NAME=demo-<lang>-api
FORGE_SERVICE_VERSION=0.1.0
FORGE_LOG_LEVEL=info
FORGE_ENV=development
```

Elixir also sets `RELEASE_DISTRIBUTION=none` (no distributed Erlang / epmd cookie).

Host publish pattern: `420N:8080`.

## Run

```bash
make demo DEMO=01
```

Or directly:

```bash
./demos/01-container-runtime/run.sh
```

### Manual curls

```bash
for p in 4201 4202 4203 4204 4205; do curl -sf http://127.0.0.1:$p/health/ready; echo; done
curl -sf http://127.0.0.1:4201/   # go
curl -sf http://127.0.0.1:4202/   # kotlin
curl -sf http://127.0.0.1:4203/   # rust
curl -sf http://127.0.0.1:4204/   # python
curl -sf http://127.0.0.1:4205/   # elixir
```

### Logs

```bash
docker compose -f demos/01-container-runtime/compose.yaml logs
docker compose -f demos/01-container-runtime/compose.yaml logs demo-elixir-api
```

## Local unit tests

```bash
cd demos/01-container-runtime/apps/go
go test ./...

cd demos/01-container-runtime/apps/kotlin
./gradlew test

cd demos/01-container-runtime/apps/rust
cargo test

cd demos/01-container-runtime/apps/python
python -m unittest -v test_server.py

cd demos/01-container-runtime/apps/elixir
mix test
```

Elixir tests also run during the Docker image build.

## Layout

```text
demos/01-container-runtime/
├── README.md
├── run.sh
├── compose.yaml
└── apps/
    ├── go/       # demo-go-api (no platform SDK imports)
    ├── kotlin/   # demo-kotlin-api (Ktor/Netty; no platform SDK)
    ├── rust/     # demo-rust-api (Axum/Tokio; no platform SDK)
    ├── python/   # demo-python-api (stdlib only; no platform SDK)
    └── elixir/   # demo-elixir-api (Bandit/Plug; no platform SDK)
```
