# Demo 00: Repository foundation

Verifies that the local Forge Platform infrastructure starts and is healthy.

## What this demo checks

* Docker Compose starts
* PostgreSQL accepts connections
* NATS responds
* Local OCI registry responds
* OpenTelemetry Collector is healthy
* Grafana is reachable

## Run

```bash
make setup
make demo DEMO=00
```

Or directly:

```bash
./demos/00-foundation/run.sh
```
