# forge-gateway

HTTP edge gateway for Forge Platform (Go + `net/http` + `httputil.ReverseProxy`).

This step (`05.02`) adds an in-memory route table, host/path matching, round-robin
upstream selection, and an admin API to replace routes at runtime. Control sync,
health-aware skipping, request IDs/timeouts, and WebSocket/SSE arrive later.

## Quick start

From the repository root:

```bash
make service-run SERVICE=forge-gateway
curl -sf http://127.0.0.1:4000/health/live
curl -sf http://127.0.0.1:4000/health/ready
```

Or from this directory:

```bash
make run
make test
```

Local binary (no Docker):

```bash
make dev
```

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` (in container) | Host publishes `4000:8080`. Required; invalid values fail startup. |
| `FORGE_SERVICE_NAME` | `forge-gateway` | |
| `FORGE_SERVICE_VERSION` | `0.1.0` | |
| `FORGE_LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `FORGE_ENV` | `development` | |
| `FORGE_AUTH_MODE` | `dev` | Edge auth deferred to epic 09. |
| `FORGE_SHUTDOWN_GRACE_SECONDS` | `10` | Compose `stop_grace_period` should be ≥ this. |
| `FORGE_GATEWAY_STATIC_ROUTES` | _(empty)_ | Optional path to a JSON route array loaded at boot. |

Reserved for later steps (not read yet): `FORGE_CONTROL_URL`,
`FORGE_RUNTIME_URL`, `FORGE_ROUTE_SYNC_INTERVAL_SECONDS`.

## Health

| Path | Behavior |
|---|---|
| `GET /health/live` | `200` with `{"status":"ok"}` while the process is up. |
| `GET /health/ready` | `200` once the HTTP listener is accepting; `503` before that. |

## Routing

Incoming requests (other than health/admin) are matched against the in-memory
route table:

1. Host-specific routes beat host-wildcard (empty `host`) routes.
2. Within a tier, the longest matching `pathPrefix` wins.
3. Matched traffic is reverse-proxied to an upstream chosen by round-robin.
4. No match → `404` with `{"error":{"code":"no_route",...}}`.
5. Upstream connection errors → `502` with `{"error":{"code":"bad_gateway",...}}`.

Only configured upstream URLs are targeted (no open-proxy / client-chosen targets).

### Admin API (dev, unauthenticated until epic 09)

| Method | Path | Behavior |
|---|---|---|
| `GET /admin/routes` | Current route snapshot (JSON array). |
| `PUT /admin/routes` | Atomically replace the table; body is a JSON array of routes. |

Route object:

```json
{
  "host": "go.demo.localhost",
  "pathPrefix": "/",
  "upstreams": [{"url": "http://127.0.0.1:49173"}],
  "strategy": "round_robin"
}
```

Example:

```bash
# point a route at a local upstream
curl -sf -X PUT http://127.0.0.1:4000/admin/routes -H 'content-type: application/json' \
  -d '[{"host":"go.demo.localhost","upstreams":[{"url":"http://127.0.0.1:49173"}],"strategy":"round_robin"}]'
curl -sf -H 'Host: go.demo.localhost' http://127.0.0.1:4000/
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: nope.localhost' http://127.0.0.1:4000/   # 404
```

## Security

* No edge authentication yet (`FORGE_AUTH_MODE=dev`).
* `/admin/routes` is intentionally unauthenticated in dev; protect in epic 09.
* Upstream URLs are validated (`http`/`https` + host required) before entering the table.
* The gateway never proxies to arbitrary client-supplied targets.

## Observability

Structured JSON logs (`timestamp`, `level`, `service`, `message`). Proxied
requests log matched route, chosen upstream, status, and duration. Route-table
replacements log the new route count.

## Development

```bash
make test-unit          # config, matcher, table, proxy, admin contract tests
make test-integration   # Compose build/run, health, proxy smoke, SIGTERM exit 0
make lint
make format
```
