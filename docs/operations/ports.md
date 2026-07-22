# Port map

| Range | Use |
|---|---|
| 3000–3099 | dashboards and development UIs |
| 4000–4099 | platform public APIs |
| 4100–4199 | internal platform APIs |
| 4200–4299 | demo applications |
| 4300–4399 | AI and model services |
| 5000–5099 | infrastructure |

## Foundation allocations

| Service | Host port |
|---|---:|
| OCI registry | 5000 |
| PostgreSQL | 5001 |
| NATS client | 5002 |
| NATS monitoring | 5003 |
| Grafana | 3000 |
| Prometheus | 3001 |
| Tempo | 3002 |
| Loki | 3003 |
| OTLP gRPC | 4317 |
| OTLP HTTP | 4318 |
| OTEL health | 13133 |

## Platform public APIs

| Service | Host port |
|---|---:|
| Forge Gateway | 4000 |
| Forge Control | 4001 |
| Forge Identity | 4002 |

## Internal platform APIs

| Service | Host port |
|---|---:|
| Forge Runtime | 4102 |
| Forge Build | 4103 |

## Reserved for the standalone-cloud phase (epics 20–43)

Not yet allocated — reserved so future service skeletons stay consistent. Plan:
[`FUTURE_PLAN.md`](../implementation/FUTURE_PLAN.md).

| Service | Host port | Epic |
|---|---:|---|
| Forge Console (web UI) | 3010 | 40 |
| Forge Scheduler (if extracted from Control) | 4108 | 08 / 39 |
| Forge Discovery | 4109 | 21 |
| Forge Network | 4110 | 22 |
| Forge Infrastructure | 4111 | 23 |
| Forge Autoscaler | 4112 | 24 |
| Forge Registry | 4113 | 26 |
| Forge Deploy | 4114 | 27 |
| Forge Queue | 4115 | 28 |
| Forge Data (database controller) | 4116 | 29 |
| Forge Volumes | 4117 | 30 |
| Forge Alerts | 4118 | 37 |
| Forge Backup | 4119 | 36 |
| Forge Policy | 4120 | 33 |
| Forge DNS (API) | 4121 | 34 |
| Forge DNS (resolver, udp) | 5053 | 21 / 34 |
| Forge Certificates | 4122 | 34 |
| Forge Usage | 4123 | 41 |

## Demo allocations (epic 01)

Reserved host ports for `demos/01-container-runtime` (five-language runtime contract suite):

| Demo app | Host port |
|---|---:|
| Go | 4201 |
| Kotlin | 4202 |
| Rust | 4203 |
| Python | 4204 |
| Elixir | 4205 |
