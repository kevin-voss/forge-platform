# Capstone Gateway hostnames (19.02)

Gateway uses `FORGE_HOST_PATTERN={service}.demo.localhost` (see `compose.yaml`).
Control service names become stable hostnames — no DNS required for smoke checks:

```bash
curl -fsS -H 'Host: api.demo.localhost' http://127.0.0.1:4000/health/ready
```

| Control service | Product image service | Hostname | Role |
|---|---|---|---|
| `api` | `incident-api` | `api.demo.localhost` | Public incident CRUD |
| `admin` | `incident-admin` | `admin.demo.localhost` | Admin/config |
| `logs` | `incident-log-worker` | `logs.demo.localhost` | Log ingest + `incident.created` consumer |
| `classify` | `incident-classify` | `classify.demo.localhost` | Classification |
| `notify` | `incident-notify` | `notify.demo.localhost` | Notifications |

Routes are synced from Control/Runtime via Gateway (`POST /admin/routes/refresh`).
Unready upstreams are skipped (health-aware proxy).
