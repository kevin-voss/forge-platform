# Make targets

## Root

| Target | Purpose |
|---|---|
| `make setup` | create `.env`, chmod scripts, verify docker/curl |
| `make dev` | start foundation infra and wait |
| `make infra-up` | start Compose without extra waits helpers beyond compose |
| `make stop` | stop Compose |
| `make restart` | stop + dev |
| `make status` | `compose ps` + smoke test |
| `make logs` | tail Compose logs |
| `make test` | currently infrastructure tests |
| `make test-infrastructure` | readiness + smoke + bootstrap SQL check |
| `make lint` | `bash -n` on scripts |
| `make reset` | wipe volumes, setup, dev |
| `make demo DEMO=00` | run numbered demo |
| `make service-test SERVICE=...` | delegate to service Makefile |
| `make service-run SERVICE=...` | delegate to service Makefile |

## Service Makefiles (later)

Each service should expose:

```bash
make dev build run test test-unit test-integration lint format docker-build docker-run clean
```
