# Local infrastructure

Step `00.01` establishes the local substrate used by all later services.

## Topology

```text
Developer tools (Make, curl, scripts)
            │
            ▼
     docker compose
   ┌───────────────────────────────┐
   │ postgres · nats · registry    │
   │ otel-collector                │
   │ prometheus · tempo · loki     │
   │ grafana                       │
   └───────────────────────────────┘
```

## Lifecycle

| Command | Effect |
|---|---|
| `make setup` | ensure `.env`, executable scripts, tooling checks |
| `make infra-up` / `make dev` | start foundation stack and wait for readiness |
| `make status` | Compose ps + smoke checks |
| `make stop` | stop containers |
| `make reset` | destroy volumes and recreate |

## Data persistence

Compose named volumes:

* `forge-postgres-data`
* `forge-nats-data`
* `forge-registry-data`
* `forge-prometheus-data`
* `forge-tempo-data`
* `forge-loki-data`
* `forge-grafana-data`

## Config sources

| Path | Role |
|---|---|
| `compose.yaml` | service definitions + healthchecks |
| `infrastructure/postgres/init/` | bootstrap SQL |
| `infrastructure/nats/nats.conf` | NATS + JetStream |
| `infrastructure/registry/config.yml` | OCI registry |
| `infrastructure/otel/config.yaml` | telemetry pipelines |
| `infrastructure/prometheus/prometheus.yml` | scrape config |
| `infrastructure/tempo/tempo.yaml` | trace backend |
| `infrastructure/loki/loki.yaml` | log backend |
| `infrastructure/grafana/provisioning/` | datasources/dashboards |

## Hybrid mode (later services)

```bash
make infra-up
cd services/<service>
make dev
```

Infrastructure stays in Compose; one service runs on the host for fast iteration.
