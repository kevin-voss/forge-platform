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
| Forge Control | 4001 |

## Internal platform APIs

| Service | Host port |
|---|---:|
| Forge Runtime | 4102 |

## Demo allocations (epic 01)

Reserved host ports for `demos/01-container-runtime` (five-language runtime contract suite):

| Demo app | Host port |
|---|---:|
| Go | 4201 |
| Kotlin | 4202 |
| Rust | 4203 |
| Python | 4204 |
| Elixir | 4205 |
