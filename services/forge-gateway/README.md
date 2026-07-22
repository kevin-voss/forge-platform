# forge-gateway

HTTP edge gateway for Forge Platform (Go + `net/http` + `httputil.ReverseProxy`).

This step (`05.03`) syncs the in-memory route table from platform state: Control
`GET /v1/endpoints` when available, otherwise Runtime `GET /v1/node/state` joined
with Control project/service metadata for hostnames. Health-aware skipping,
request IDs/timeouts, and WebSocket/SSE arrive later.

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
| `FORGE_CONTROL_URL` | _(empty)_ | Control base URL; required to enable sync. |
| `FORGE_RUNTIME_URL` | _(empty)_ | Runtime base URL; used as interim endpoint source / fallback. |
| `FORGE_ROUTE_SOURCE` | `control` | `control` (primary + Runtime fallback on 404) or `runtime`. |
| `FORGE_ROUTE_SYNC_INTERVAL_SECONDS` | `10` | Poll interval; `0` disables the background loop (refresh still works). |
| `FORGE_HOST_PATTERN` | `{service}.{project}.demo.localhost` | Hostname template for derived routes. |
| `FORGE_UPSTREAM_HOST` | `127.0.0.1` | Host paired with Runtime-published ports. Compose uses `host.docker.internal`. |

Sync is enabled when `FORGE_CONTROL_URL` is set (`runtime` source also requires `FORGE_RUNTIME_URL`).

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

### Route sync (05.03)

On an interval (and via refresh), the gateway:

1. Fetches active endpoints from Control `GET /v1/endpoints` (documented contract).
2. If that read model is missing (`404`/`405`), falls back to Runtime
   `GET /v1/node/state` and joins Control project trees for `{service}` / `{project}` names.
3. Derives routes (`host` + upstream URLs) and atomically replaces the table.
4. On source failure, keeps the last-good table and logs a warning.

Expected Control endpoints shape (when implemented):

```json
[
  {
    "host": "api.acme.demo.localhost",
    "service": "api",
    "project": "acme",
    "upstreams": [{"url": "http://host.docker.internal:49173"}],
    "ready": true
  }
]
```

### Admin API (dev, unauthenticated until epic 09)

| Method | Path | Behavior |
|---|---|---|
| `GET /admin/routes` | Current route snapshot (JSON array). |
| `PUT /admin/routes` | Atomically replace the table; body is a JSON array of routes. |
| `POST /admin/routes/refresh` | Force one sync now; `200` `{"routesLoaded":N,"ok":true,...}`. |

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
# after a Runtime-deployed workload (or force refresh)
curl -sf -X POST http://127.0.0.1:4000/admin/routes/refresh
curl -sf http://127.0.0.1:4000/admin/routes | python3 -m json.tool
curl -sf -H 'Host: api.acme.demo.localhost' http://127.0.0.1:4000/

# manual override still works
curl -sf -X PUT http://127.0.0.1:4000/admin/routes -H 'content-type: application/json' \
  -d '[{"host":"go.demo.localhost","upstreams":[{"url":"http://127.0.0.1:49173"}],"strategy":"round_robin"}]'
```

## Security

* No edge authentication yet (`FORGE_AUTH_MODE=dev`).
* `/admin/routes` is intentionally unauthenticated in dev; protect in epic 09.
* Upstream URLs are validated (`http`/`https` + host required) before entering the table.
* Only endpoints from trusted Control/Runtime sources become routes (no client-driven injection on the data plane).

## Observability

Structured JSON logs (`timestamp`, `level`, `service`, `message`). Proxied
requests log matched route, chosen upstream, status, and duration. Sync cycles
log source, routes built, and added/removed host diffs. Sync failures retain the
last-good table.

## Development

```bash
make test-unit          # config, matcher, table, proxy, sync, admin contract tests
make test-integration   # Compose build/run, health, refresh sync, proxy smoke, SIGTERM exit 0
make lint
make format
```
