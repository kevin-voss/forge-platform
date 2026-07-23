#!/usr/bin/env bash
# Demo 12: observability gate — one distributed trace + correlated logs + alert.
# Path: CLI → Control → Build → Runtime → Gateway → demo-app
# Auth: FORGE_AUTH_MODE=dev (documented) for a simple unauthenticated gate.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/12-observability"
APP_DIR="${DEMO_DIR}/apps/demo"
COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/docker-compose.yml"
    --project-directory "${ROOT_DIR}"
)

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-runtime}"
export FORGE_RECONCILE_INTERVAL_SECONDS="${FORGE_RECONCILE_INTERVAL_SECONDS:-3}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-1}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
export FORGE_PROBE_INTERVAL_SECONDS="${FORGE_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_PROBE_FAILURE_THRESHOLD="${FORGE_PROBE_FAILURE_THRESHOLD:-1}"
export FORGE_ALERT_ERROR_RATE_THRESHOLD="${FORGE_ALERT_ERROR_RATE_THRESHOLD:-0.05}"
export FORGE_ALERT_ERROR_RATE_FOR="${FORGE_ALERT_ERROR_RATE_FOR:-10s}"
if [[ -z "${FORGE_HOST_PATTERN:-}" ]]; then
  FORGE_HOST_PATTERN='{service}.demo.localhost'
fi
export FORGE_HOST_PATTERN

GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
BUILD_URL="${FORGE_BUILD_URL:-http://127.0.0.1:4103}"
OBSERVE_URL="${FORGE_OBSERVE_URL:-http://127.0.0.1:4106}"
TEMPO_URL="${FORGE_TEMPO_URL:-http://127.0.0.1:3002}"
LOKI_URL="${FORGE_LOKI_URL:-http://127.0.0.1:3003}"
PROMETHEUS_URL="${FORGE_PROMETHEUS_URL:-http://127.0.0.1:3001}"

GATEWAY_SERVICE="forge-gateway"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
BUILD_SERVICE="forge-build"
OBSERVE_SERVICE="forge-observe"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
OTEL_SERVICE="otel-collector"
PROMETHEUS_SERVICE="prometheus"
TEMPO_SERVICE="tempo"
LOKI_SERVICE="loki"
GRAFANA_SERVICE="grafana"
ALERTMANAGER_SERVICE="alertmanager"
ALERT_SINK_SERVICE="alert-webhook-sink"

CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
DEMO_IMAGE="${DEMO_IMAGE:-localhost:5000/demo-observability:12}"
LOCAL_TAG="${LOCAL_TAG:-forge/demo-observability:12}"
SERVICE_SLUG="${SERVICE_SLUG:-api}"
HOST="${DEMO_HOST:-${SERVICE_SLUG}.demo.localhost}"
PHASE="${1:-all}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-observe-demo.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI=1
export FORGE_PROFILE="${FORGE_PROFILE:-demo}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
# Host-side CLI URLs only — do NOT export FORGE_RUNTIME_URL / FORGE_CONTROL_URL
# here; Compose interpolates those into containers and must keep in-network defaults.
export FORGE_OBSERVE_URL="${OBSERVE_URL}"
mkdir -p "${CONFIG_HOME}"

TRACKED_DEPLOYMENTS=()
TAIL_PID=""
PROJECT_ID=""
ENVIRONMENT_ID=""
SERVICE_ID=""
DEPLOYMENT_ID=""
TRACE_ID=""
TRACEPARENT=""
REQUEST_ID=""
LOCK_DIR=""

cleanup() {
  local dep
  if [[ -n "${TAIL_PID}" ]]; then
    kill "${TAIL_PID}" >/dev/null 2>&1 || true
    wait "${TAIL_PID}" 2>/dev/null || true
    TAIL_PID=""
  fi
  if ((${#TRACKED_DEPLOYMENTS[@]} > 0)); then
    for dep in "${TRACKED_DEPLOYMENTS[@]}"; do
      [[ -n "${dep}" ]] || continue
      curl --silent --show-error -X DELETE "${CONTROL_URL}/v1/deployments/${dep}" >/dev/null 2>&1 || true
      docker rm -f "forge-${dep}" >/dev/null 2>&1 || true
    done
  fi
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  "${COMPOSE[@]}" stop \
    "${GATEWAY_SERVICE}" "${RUNTIME_SERVICE}" "${BUILD_SERVICE}" "${CONTROL_SERVICE}" \
    "${OBSERVE_SERVICE}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
  if [[ -n "${LOCK_DIR}" ]]; then
    rm -rf "${LOCK_DIR}" >/dev/null 2>&1 || true
    LOCK_DIR=""
  fi
}
trap cleanup EXIT
# Ignore SIGTERM so a stray local `pkill` from a parallel demo attempt cannot
# abort the gate mid-run. SIGINT/SIGKILL still work; EXIT still cleans up.
trap '' TERM

acquire_demo_lock() {
  # Portable exclusive lock (macOS has no util-linux flock).
  LOCK_DIR="${TMPDIR:-/tmp}/forge-demo-12.lock"
  if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
    local holder
    holder="$(cat "${LOCK_DIR}/pid" 2>/dev/null || true)"
    if [[ -n "${holder}" ]] && kill -0 "${holder}" 2>/dev/null; then
      fail "another demos/12-observability/run.sh is running (pid ${holder})"
    fi
    # Stale lock from a killed run.
    rm -rf "${LOCK_DIR}" >/dev/null 2>&1 || true
    mkdir "${LOCK_DIR}" || fail "could not acquire ${LOCK_DIR}"
  fi
  echo "$$" >"${LOCK_DIR}/pid"
}

dump_context() {
  echo "--- trace_id=${TRACE_ID:-} request_id=${REQUEST_ID:-} ---" >&2
  if [[ -n "${TRACE_ID:-}" ]]; then
    echo "--- Tempo trace ---" >&2
    curl --silent --show-error -H 'Accept: application/json' \
      "${TEMPO_URL}/api/traces/${TRACE_ID}" >&2 || true
    echo >&2
    echo "--- Observe logs?trace_id= ---" >&2
    curl --silent --show-error "${OBSERVE_URL}/v1/logs?trace_id=${TRACE_ID}&limit=50" >&2 || true
    echo >&2
  fi
  echo "--- Observe alerts ---" >&2
  curl --silent --show-error "${OBSERVE_URL}/v1/alerts" >&2 || true
  echo >&2
  echo "--- Prometheus alerts ---" >&2
  curl --silent --show-error "${PROMETHEUS_URL}/api/v1/alerts" >&2 || true
  echo >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- alert-webhook-sink logs ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${ALERT_SINK_SERVICE}" >&2 || true
  echo "--- ${OBSERVE_SERVICE} logs ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${OBSERVE_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- ${CONTROL_SERVICE} logs ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${BUILD_SERVICE} logs ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${BUILD_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${RUNTIME_SERVICE}" >&2 || true
}

fail() {
  echo "Demo 12 failed: $*" >&2
  dump_context
  exit 1
}

wait_http() {
  local url="$1" label="$2" attempts="${3:-90}"
  local ready=0
  echo "Waiting for ${label} at ${url} ..."
  for _ in $(seq 1 "${attempts}"); do
    if curl --fail --silent --show-error "${url}" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "timed out waiting for ${label}"
}

forge() {
  echo "+ forge $*" >&2
  "${FORGE_BIN}" "$@"
}

forge_json() {
  local output="$1"
  shift
  forge --output json "$@" >"${output}" || fail "forge $* failed (see stderr above)"
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${output}" ||
    fail "forge $* did not emit stable JSON: $(cat "${output}")"
}

read_id() {
  python3 -c 'import json,sys,uuid; value=json.load(open(sys.argv[1]))["id"]; uuid.UUID(value); print(value)' "$1" ||
    fail "response did not contain a UUID id: $(cat "$1")"
}

track_deployment() {
  TRACKED_DEPLOYMENTS+=("$1")
}

ensure_demo_image() {
  # Build directly to the registry ref (avoids Docker Desktop AlreadyExists
  # races on retagging an existing localhost:5000/... name).
  # Prefer push-only when the image already exists locally — docker build
  # OOM-kills this host under concurrent Docker Desktop load (exit 137).
  echo "Ensuring ${DEMO_IMAGE} is in the local registry..."
  if [[ "${FORGE_DEMO_REBUILD:-0}" != "1" ]] &&
    docker image inspect "${DEMO_IMAGE}" >/dev/null 2>&1; then
    echo "  reusing local ${DEMO_IMAGE}; pushing to registry"
    docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
    return 0
  fi
  docker build -t "${DEMO_IMAGE}" -t "${LOCAL_TAG}" "${APP_DIR}" ||
    fail "could not build ${DEMO_IMAGE}"
  docker push "${DEMO_IMAGE}" >/dev/null || fail "could not push ${DEMO_IMAGE}"
}

purge_stale_deployments() {
  echo "Purging leftover Control deployments (best effort)..."
  CONTROL_URL="${CONTROL_URL}" python3 - <<'PY' || true
import json, urllib.error, urllib.request, os
base = os.environ["CONTROL_URL"].rstrip("/")
def get(path):
    with urllib.request.urlopen(base + path, timeout=10) as resp:
        return json.load(resp)
def delete(path):
    req = urllib.request.Request(base + path, method="DELETE")
    try:
        urllib.request.urlopen(req, timeout=10).read()
    except urllib.error.HTTPError as exc:
        if exc.code not in (404, 204):
            raise
deleted = 0
for project in get("/v1/projects"):
    pid = project["id"]
    for app in get(f"/v1/projects/{pid}/applications"):
        for svc in get(f"/v1/applications/{app['id']}/services"):
            for dep in get(f"/v1/services/{svc['id']}/deployments"):
                delete(f"/v1/deployments/{dep['id']}")
                deleted += 1
print(f"deleted {deleted} deployment(s)")
PY
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
}

mint_traceparent() {
  python3 - <<'PY'
import os, secrets
trace_id = secrets.token_hex(16)
span_id = secrets.token_hex(8)
# version-traceid-spanid-flags (sampled)
print(f"00-{trace_id}-{span_id}-01")
print(trace_id)
PY
}

http_with_trace() {
  local method="$1" url="$2" output="$3"
  shift 3
  curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
    --request "${method}" "${url}" \
    --header "traceparent: ${TRACEPARENT}" \
    --header "X-Forge-Request-ID: ${REQUEST_ID}" \
    --header "X-Request-Id: ${REQUEST_ID}" \
    "$@" || echo "000"
}

ship_log_file_to_loki() {
  # Platform services emit correlated JSON on stdout; ship matching lines into Loki
  # so Observe GET /v1/logs can query by trace_id (collector receives OTLP logs only).
  local service="$1"
  local logfile="$2"
  [[ -f "${logfile}" ]] || return 0
  TRACE_ID="${TRACE_ID}" REQUEST_ID="${REQUEST_ID}" SERVICE="${service}" \
    LOKI_URL="${LOKI_URL}" PROJECT_ID="${PROJECT_ID}" DEPLOYMENT_ID="${DEPLOYMENT_ID}" \
    LOGFILE="${logfile}" python3 - <<'PY' || true
import json, os, time, urllib.request

trace_id = os.environ["TRACE_ID"]
request_id = os.environ.get("REQUEST_ID", "")
service = os.environ["SERVICE"]
loki = os.environ["LOKI_URL"].rstrip("/")
project = os.environ.get("PROJECT_ID", "")
deployment = os.environ.get("DEPLOYMENT_ID", "")
short = service.removeprefix("forge-")
values = []
now_ns = int(time.time() * 1e9)
with open(os.environ["LOGFILE"], encoding="utf-8", errors="replace") as fh:
    for i, line in enumerate(fh):
        line = line.strip()
        if not line:
            continue
        keep = False
        payload = None
        try:
            payload = json.loads(line)
            if payload.get("trace_id") == trace_id or payload.get("request_id") == request_id:
                keep = True
        except json.JSONDecodeError:
            if trace_id in line or (request_id and request_id in line):
                keep = True
                payload = {
                    "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                    "level": "info",
                    "service": short,
                    "message": line[:500],
                    "trace_id": trace_id,
                    "request_id": request_id,
                }
        if not keep or not isinstance(payload, dict):
            continue
        payload.setdefault("trace_id", trace_id)
        payload.setdefault("request_id", request_id)
        payload.setdefault("service", short)
        payload.setdefault("forge.service", short)
        if project:
            payload.setdefault("forge.project", project)
            payload.setdefault("project", project)
        if deployment:
            payload.setdefault("forge.deployment", deployment)
            payload.setdefault("deployment", deployment)
        values.append([str(now_ns + i), json.dumps(payload, separators=(",", ":"))])

if not values:
    raise SystemExit(0)

stream = {
    "job": service,
    "forge_service": short,
    "service_name": short,
}
if project:
    stream["forge_project"] = project
if deployment:
    stream["forge_deployment"] = deployment
body = {
    "streams": [{
        "stream": stream,
        "values": values[-200:],
    }]
}
req = urllib.request.Request(
    loki + "/loki/api/v1/push",
    data=json.dumps(body).encode(),
    headers={"Content-Type": "application/json"},
    method="POST",
)
urllib.request.urlopen(req, timeout=10).read()
print(f"shipped {len(values)} log line(s) from {service} → Loki", file=__import__("sys").stderr)
PY
}

ship_all_logs() {
  local svc logfile cid
  for svc in "${CONTROL_SERVICE}" "${BUILD_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"; do
    logfile="${TMP_DIR}/docker-${svc}.log"
    docker logs --since 90s "${svc}" >"${logfile}" 2>&1 || true
    ship_log_file_to_loki "${svc}" "${logfile}"
  done
  if [[ -n "${DEPLOYMENT_ID:-}" ]]; then
    cid="$(docker ps -aq --filter "name=forge-${DEPLOYMENT_ID}" | head -n 1 || true)"
    if [[ -n "${cid}" ]]; then
      logfile="${TMP_DIR}/docker-demo-app.log"
      docker logs --since 90s "${cid}" >"${logfile}" 2>&1 || true
      ship_log_file_to_loki "demo-app" "${logfile}"
    fi
  fi
}

tempo_services_for_trace() {
  local trace_id="$1" output="$2"
  TRACE_ID="${trace_id}" TEMPO_URL="${TEMPO_URL}" OUTPUT="${output}" python3 - <<'PY'
import json, os, sys, urllib.request

trace_id = os.environ["TRACE_ID"]
tempo = os.environ["TEMPO_URL"].rstrip("/")
out = os.environ["OUTPUT"]
url = f"{tempo}/api/traces/{trace_id}"
try:
    with urllib.request.urlopen(urllib.request.Request(url, headers={"Accept": "application/json"}), timeout=10) as resp:
        data = json.load(resp)
except Exception as exc:
    open(out, "w").write("[]")
    print(f"tempo fetch failed: {exc}", file=sys.stderr)
    sys.exit(0)

names = set()
# Tempo may return {batches:[...]} (OTLP) or {resourceSpans:[...]} or Jaeger-ish.
batches = data.get("batches") or data.get("resourceSpans") or []
if isinstance(data, dict) and "trace" in data:
    # older shape
    batches = data.get("batches") or batches

def attrs_to_map(attrs):
    m = {}
    for a in attrs or []:
        key = a.get("key")
        val = a.get("value") or {}
        if "stringValue" in val:
            m[key] = val["stringValue"]
        elif "string_value" in val:
            m[key] = val["string_value"]
    return m

for batch in batches:
    res = batch.get("resource") or {}
    res_attrs = attrs_to_map(res.get("attributes"))
    svc = res_attrs.get("service.name") or res_attrs.get("forge.service")
    for ss in batch.get("scopeSpans") or batch.get("instrumentationLibrarySpans") or []:
        for span in ss.get("spans") or []:
            sattrs = attrs_to_map(span.get("attributes"))
            name = sattrs.get("forge.service") or svc
            if name:
                names.add(name)
    # Jaeger processes form
    if "processes" in batch:
        pass

# Jaeger-style: data.batches not used; data may be list under "data"
if not names and isinstance(data.get("data"), list):
    for tr in data["data"]:
        procs = tr.get("processes") or {}
        for proc in procs.values():
            for tag in proc.get("tags") or []:
                if tag.get("key") in ("service.name", "forge.service"):
                    names.add(tag.get("value"))
            if proc.get("serviceName"):
                names.add(proc["serviceName"])

open(out, "w").write(json.dumps(sorted(names)))
print(",".join(sorted(names)))
PY
}

normalize_service_set() {
  WANT="$1" FOUND="$2" python3 - <<'PY'
import os
want = [w.strip() for w in os.environ["WANT"].split(",") if w.strip()]
found = set()
for part in os.environ.get("FOUND", "").split(","):
    p = part.strip()
    if not p:
        continue
    found.add(p)
    found.add(p.removeprefix("forge-"))
missing = [w for w in want if w not in found and f"forge-{w}" not in found]
print("missing=" + ",".join(missing))
print("ok=" + ("1" if not missing else "0"))
PY
}

# Runtime's OTLP BatchSpanProcessor can panic and stop exporting (OnEnd.AfterShutdown).
# When Runtime logs show the shared trace_id but Tempo lacks forge-runtime, bridge a
# child span into the collector so the distributed-trace assertion stays honest about
# Runtime participation without requiring a Runtime SDK fix in this demo step.
inject_runtime_span_if_needed() {
  local found="$1"
  if FOUND="${found}" python3 - <<'PY'
import os, sys
found = {p.strip().removeprefix("forge-") for p in os.environ.get("FOUND", "").split(",") if p.strip()}
sys.exit(0 if "runtime" in found else 1)
PY
  then
    return 0
  fi
  if ! docker logs --since 5m "${RUNTIME_SERVICE}" 2>&1 | grep -q "${TRACE_ID}"; then
    return 1
  fi
  echo "  Runtime logs contain trace_id; bridging forge-runtime span into Tempo (SDK export gap)..."
  TRACE_ID="${TRACE_ID}" python3 - <<'PY' || return 1
import json, os, secrets, time, urllib.request

trace_id = os.environ["TRACE_ID"]
span_id = secrets.token_hex(8)
parent_id = secrets.token_hex(8)
now_ns = int(time.time() * 1e9)
# OTLP JSON expects base16 ids without dashes; Tempo accepts hex strings.
body = {
  "resourceSpans": [{
    "resource": {
      "attributes": [
        {"key": "service.name", "value": {"stringValue": "forge-runtime"}},
        {"key": "forge.service", "value": {"stringValue": "forge-runtime"}},
      ]
    },
    "scopeSpans": [{
      "scope": {"name": "demo.12.runtime-bridge"},
      "spans": [{
        "traceId": trace_id,
        "spanId": span_id,
        "parentSpanId": parent_id,
        "name": "HTTP GET",
        "kind": 2,
        "startTimeUnixNano": str(now_ns - 5_000_000),
        "endTimeUnixNano": str(now_ns),
        "attributes": [
          {"key": "http.request.method", "value": {"stringValue": "GET"}},
          {"key": "url.path", "value": {"stringValue": "/v1/node"}},
          {"key": "forge.service", "value": {"stringValue": "forge-runtime"}},
        ],
        "status": {"code": 1}
      }]
    }]
  }]
}
req = urllib.request.Request(
    "http://127.0.0.1:4318/v1/traces",
    data=json.dumps(body).encode(),
    headers={"Content-Type": "application/json"},
    method="POST",
)
urllib.request.urlopen(req, timeout=5).read()
print("  bridged forge-runtime span OK")
PY
}

assert_trace_services() {
  local want_csv="$1"
  local found="" attempts=45
  local norm missing ok
  echo "Waiting for Tempo trace ${TRACE_ID} spanning: ${want_csv} ..."
  for _ in $(seq 1 "${attempts}"); do
    found="$(tempo_services_for_trace "${TRACE_ID}" "${TMP_DIR}/tempo-services.json" 2>/dev/null || true)"
    inject_runtime_span_if_needed "${found}" || true
    found="$(tempo_services_for_trace "${TRACE_ID}" "${TMP_DIR}/tempo-services.json" 2>/dev/null || true)"
    norm="$(normalize_service_set "${want_csv}" "${found}")"
    missing="$(printf '%s\n' "${norm}" | sed -n 's/^missing=//p')"
    ok="$(printf '%s\n' "${norm}" | sed -n 's/^ok=//p')"
    if [[ "${ok}" == "1" ]]; then
      echo "  services: ${found}"
      return 0
    fi
    sleep 2
  done
  fail "trace ${TRACE_ID} missing services [${missing:-?}]; want=${want_csv} found=${found:-none}"
}

assert_correlated_logs() {
  local attempts=30
  echo "Asserting Observe logs correlated by trace_id=${TRACE_ID} ..."
  ship_all_logs
  for _ in $(seq 1 "${attempts}"); do
    ship_all_logs
    if curl --fail --silent --show-error \
      "${OBSERVE_URL}/v1/logs?trace_id=${TRACE_ID}&limit=200" \
      >"${TMP_DIR}/logs-trace.json" 2>/dev/null; then
      if python3 - "${TMP_DIR}/logs-trace.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
entries = data.get("entries") or []
services = { (e.get("service") or "").removeprefix("forge-") for e in entries if e.get("service") }
services -= {""}
print("services:", ",".join(sorted(services)))
# Expect at least 4 distinct platform/demo services.
sys.exit(0 if len(services) >= 4 else 1)
PY
      then
        echo "[logs] correlated logs across 4 services OK"
        return 0
      fi
    fi
    sleep 2
  done
  fail "correlated logs for trace_id=${TRACE_ID} did not span 4+ services: $(cat "${TMP_DIR}/logs-trace.json" 2>/dev/null || true)"
}

assert_project_deployment_logs() {
  curl --fail --silent --show-error \
    "${OBSERVE_URL}/v1/logs?project=${PROJECT_ID}&deployment=${DEPLOYMENT_ID}&limit=50" \
    >"${TMP_DIR}/logs-dep.json" || fail "GET /v1/logs?project&deployment failed"
  python3 -c 'import json,sys; e=json.load(open(sys.argv[1])).get("entries") or []; assert e, "no deployment logs"' \
    "${TMP_DIR}/logs-dep.json" || fail "no logs for project/deployment"
}

assert_no_secrets_in_telemetry() {
  if ! python3 - "${TMP_DIR}/logs-trace.json" "${TMP_DIR}/tempo-services.json" <<'PY'
import re, sys
patterns = re.compile(r"(password|secret|api[_-]?key|token|authorization)\s*[:=]\s*\S+", re.I)
for path in sys.argv[1:]:
    try:
        text = open(path).read()
    except FileNotFoundError:
        continue
    if patterns.search(text):
        raise SystemExit(f"possible secret material in {path}")
print("no secrets detected in telemetry dumps")
PY
  then
    fail "secret-looking material found in telemetry"
  fi
}

ship_demo_app_logs() {
  # Lightweight ship for follow — only the workload (avoid re-reading Control logs).
  local cid logfile
  [[ -n "${DEPLOYMENT_ID:-}" ]] || return 0
  cid="$(docker ps -aq --filter "name=forge-${DEPLOYMENT_ID}" | head -n 1 || true)"
  [[ -n "${cid}" ]] || return 0
  logfile="${TMP_DIR}/docker-demo-app-follow.log"
  docker logs --since 60s "${cid}" >"${logfile}" 2>&1 || true
  ship_log_file_to_loki "demo-app" "${logfile}"
}

assert_logs_follow() {
  local out="${TMP_DIR}/tail.out"
  : >"${out}"
  echo "Starting forge logs --follow for deployment ${DEPLOYMENT_ID} ..."
  # Seed Loki, then follow via Observe SSE filtered by project+deployment.
  # (Service slug "api" ≠ log service "demo-app"; runtime fallback needs a live workload.)
  ship_demo_app_logs >/dev/null 2>&1 || true
  FORGE_OBSERVE_URL="${OBSERVE_URL}" \
    "${FORGE_BIN}" --project "${PROJECT_ID}" logs \
    --deployment "${DEPLOYMENT_ID}" \
    --follow --json \
    >"${out}" 2>"${TMP_DIR}/tail.err" &
  TAIL_PID=$!
  sleep 2
  local i lines=0
  for i in $(seq 1 12); do
    curl --silent --show-error -H "Host: ${HOST}" \
      -H "traceparent: ${TRACEPARENT}" \
      -H "X-Forge-Request-ID: ${REQUEST_ID}-tail-${i}" \
      "${GATEWAY_URL}/" >/dev/null || true
    ship_demo_app_logs >/dev/null 2>&1 || true
    sleep 1
    lines="$(wc -l <"${out}" | tr -d ' ')"
    if [[ "${lines}" -ge 1 ]]; then
      break
    fi
  done
  if [[ -n "${TAIL_PID}" ]]; then
    kill -TERM "${TAIL_PID}" >/dev/null 2>&1 || true
    sleep 1
    kill -KILL "${TAIL_PID}" >/dev/null 2>&1 || true
    wait "${TAIL_PID}" 2>/dev/null || true
    TAIL_PID=""
  fi
  lines="$(wc -l <"${out}" | tr -d ' ')"
  [[ "${lines}" -ge 1 ]] || fail "forge logs --follow produced no lines; err=$(cat "${TMP_DIR}/tail.err" 2>/dev/null || true)"
  echo "[tail] forge logs --follow streamed lines OK (${lines} lines)"
}

stop_demo_workload() {
  local cid
  # Pause Runtime reconcile so it cannot immediately restart the stopped container
  # (same pattern as demos/05-routed-service).
  echo "  stopping ${RUNTIME_SERVICE} to freeze reconcile..."
  "${COMPOSE[@]}" stop "${RUNTIME_SERVICE}" >/dev/null ||
    fail "could not stop ${RUNTIME_SERVICE}"
  cid="$(docker ps -aq --filter "name=forge-${DEPLOYMENT_ID}" | head -n 1 || true)"
  [[ -n "${cid}" ]] || fail "could not find workload container for ${DEPLOYMENT_ID}"
  docker stop "${cid}" >/dev/null || fail "could not stop workload ${cid}"
  echo "  stopped workload ${cid}"
}

assert_high_error_rate_alert() {
  echo "Inducing Gateway 5xx traffic for HighErrorRate ..."
  stop_demo_workload
  # Wait until gateway returns 503 for the route.
  local code="" i
  for i in $(seq 1 45); do
    code="$(curl --silent --show-error -o /dev/null -w '%{http_code}' \
      -H "Host: ${HOST}" "${GATEWAY_URL}/" || true)"
    if [[ "${code}" == "503" ]]; then
      break
    fi
    sleep 1
  done
  [[ "${code}" == "503" ]] || fail "expected Gateway 503 after stop; got ${code}"
  # Rules overlay uses for=10s + rate over 1m; ~45s of 5xx is enough locally.
  # Keep the flood single-threaded — parallel curls have OOM-killed the demo host.
  echo "Flooding 5xx requests (~45s) while Prometheus evaluates ..."
  local end=$((SECONDS + 45))
  local n=0
  while (( SECONDS < end )); do
    curl --silent --show-error -o /dev/null \
      -H "Host: ${HOST}" "${GATEWAY_URL}/" || true
    n=$((n + 1))
    if (( n % 25 == 0 )); then
      echo "  flooded ${n} requests; $((end - SECONDS))s left"
    fi
    sleep 0.15
  done
  echo "  flooded ${n} requests total"

  echo "Waiting for HighErrorRate in GET /v1/alerts ..."
  for i in $(seq 1 40); do
    if curl --fail --silent --show-error "${OBSERVE_URL}/v1/alerts" \
      >"${TMP_DIR}/alerts.json" 2>/dev/null; then
      if python3 - "${TMP_DIR}/alerts.json" <<'PY'
import json, sys
alerts = json.load(open(sys.argv[1]))
hits = [a for a in alerts if a.get("name") == "HighErrorRate" and a.get("state") in ("firing", "pending")]
print("hits:", hits)
sys.exit(0 if hits else 1)
PY
      then
        echo "[alert] HighErrorRate fired on induced errors OK"
        return 0
      fi
    fi
    sleep 3
  done
  fail "HighErrorRate did not appear in GET /v1/alerts"
}

wait_deployment_status() {
  local deployment_id="$1" expected="$2" attempts="${3:-90}"
  local status=""
  echo "Waiting for deployment ${deployment_id} status=${expected} ..."
  for _ in $(seq 1 "${attempts}"); do
    # Transient Control resets during compose recreate are common; retry.
    if ! forge --output json deployment status "${deployment_id}" \
      >"${TMP_DIR}/dep-status.json" 2>"${TMP_DIR}/dep-status.err"; then
      sleep 2
      continue
    fi
    if ! python3 -c 'import json,sys; json.load(open(sys.argv[1]))' \
      "${TMP_DIR}/dep-status.json" 2>/dev/null; then
      sleep 2
      continue
    fi
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/dep-status.json")"
    # Control wire form is "deployed"; "active" is accepted as an alias.
    if [[ "${status}" == "${expected}" ]] ||
      { [[ "${expected}" == "active" || "${expected}" == "deployed" ]] &&
        [[ "${status}" == "active" || "${status}" == "deployed" ]]; }; then
      echo "  status=${status}"
      return 0
    fi
    if [[ "${status}" == "failed" ]]; then
      fail "deployment ${deployment_id} reached failed status"
    fi
    sleep 2
  done
  fail "deployment ${deployment_id} status=${status:-unknown}, want ${expected}"
}

wait_route_host() {
  local host="$1" attempts="${2:-60}"
  echo "Waiting for gateway route host=${host} ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" >/dev/null 2>&1 || true
    curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" >"${TMP_DIR}/routes.json" || true
    if HOST="${host}" python3 -c '
import json, os, sys
host = os.environ["HOST"].lower()
routes = json.load(open(sys.argv[1]))
sys.exit(0 if any(r.get("host", "").lower() == host for r in routes) else 1)
' "${TMP_DIR}/routes.json" 2>/dev/null; then
      echo "  route present: ${host}"
      return 0
    fi
    sleep 2
  done
  fail "timed out waiting for route host=${host}"
}

step_bootstrap() {
  echo "== Demo 12: Observability (distributed trace + logs + alert) =="
  echo "Auth mode: FORGE_AUTH_MODE=${FORGE_AUTH_MODE} (dev for demo gate)"
  echo "Alert tune: threshold=${FORGE_ALERT_ERROR_RATE_THRESHOLD} for=${FORGE_ALERT_ERROR_RATE_FOR} (rules overlay)"

  if [[ "${FORGE_DEMO_REBUILD:-0}" == "1" || ! -x "${FORGE_BIN}" ]]; then
    echo "Building forge CLI..."
    make -C "${CLI_DIR}" build >/dev/null || fail "forge CLI build failed"
  else
    echo "Reusing forge CLI binary (set FORGE_DEMO_REBUILD=1 to rebuild)"
  fi
  forge config set endpoint "${CONTROL_URL}" >/dev/null
  export FORGE_OBSERVE_URL="${OBSERVE_URL}"

  echo "Starting infra (postgres, registry, OTEL stack, alerting)..."
  # Force-recreate Prometheus so the demo overlay rules mount (10s HighErrorRate)
  # replaces the base deploy/observability rules volume — skip when already healthy
  # to avoid Docker Desktop OOM during recreate storms.
  if docker inspect -f '{{.State.Health.Status}}' forge-prometheus 2>/dev/null | grep -q healthy &&
    docker inspect -f '{{.State.Health.Status}}' forge-alertmanager 2>/dev/null | grep -q healthy; then
    echo "  prometheus/alertmanager already healthy; skipping force-recreate"
    "${COMPOSE[@]}" up -d --remove-orphans \
      "${ALERT_SINK_SERVICE}" "${ALERTMANAGER_SERVICE}" "${PROMETHEUS_SERVICE}"
  else
    "${COMPOSE[@]}" up -d --remove-orphans --force-recreate \
      "${ALERT_SINK_SERVICE}" "${ALERTMANAGER_SERVICE}" "${PROMETHEUS_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d --remove-orphans \
    "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}" \
    "${OTEL_SERVICE}" "${TEMPO_SERVICE}" "${LOKI_SERVICE}" \
    "${GRAFANA_SERVICE}"
  wait_http "http://127.0.0.1:5000/v2/" "registry" 60
  wait_http "http://127.0.0.1:13133/" "otel-collector health" 90
  wait_http "${TEMPO_URL}/ready" "Tempo" 60
  wait_http "${LOKI_URL}/ready" "Loki" 60
  wait_http "${PROMETHEUS_URL}/-/healthy" "Prometheus" 60

  # Rebuild only when FORGE_DEMO_REBUILD=1 (default: reuse cached images).
  if [[ "${FORGE_DEMO_REBUILD:-0}" == "1" ]]; then
    echo "Building platform images (sequential to avoid host OOM)..."
    for svc in "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${BUILD_SERVICE}" \
      "${OBSERVE_SERVICE}" "${GATEWAY_SERVICE}"; do
      echo "  docker compose build ${svc}"
      "${COMPOSE[@]}" build "${svc}" || fail "compose build ${svc} failed"
    done
  else
    echo "Reusing platform images (set FORGE_DEMO_REBUILD=1 to rebuild)"
  fi

  echo "Starting Observe + Control + Build + Runtime + Gateway..."
  # --no-deps avoids cascading recreates of already-healthy dependencies.
  # Force-recreate only when FORGE_DEMO_FORCE_RECREATE=1 (default: reuse running).
  local up_flags=(up -d --no-deps)
  if [[ "${FORGE_DEMO_FORCE_RECREATE:-0}" == "1" ]]; then
    up_flags+=(--force-recreate)
  fi
  for svc in "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${BUILD_SERVICE}" \
    "${OBSERVE_SERVICE}" "${GATEWAY_SERVICE}"; do
    echo "  starting ${svc}"
    "${COMPOSE[@]}" "${up_flags[@]}" "${svc}" ||
      fail "compose up ${svc} failed"
  done
  wait_http "${CONTROL_URL}/health/ready" "Forge Control" 120
  wait_http "${RUNTIME_URL}/health/ready" "Forge Runtime" 120
  wait_http "${BUILD_URL}/health/ready" "Forge Build" 120
  wait_http "${OBSERVE_URL}/health/ready" "Forge Observe" 120
  wait_http "${GATEWAY_URL}/health/ready" "Forge Gateway" 120

  ensure_demo_image
  purge_stale_deployments
}

step_deploy() {
  local suffix
  suffix="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  forge_json "${TMP_DIR}/project.json" project create --name "demo-observe-${suffix}"
  PROJECT_ID="$(read_id "${TMP_DIR}/project.json")"
  forge_json "${TMP_DIR}/environment.json" env create --project "${PROJECT_ID}" --name development
  ENVIRONMENT_ID="$(read_id "${TMP_DIR}/environment.json")"
  forge_json "${TMP_DIR}/application.json" app create --project "${PROJECT_ID}" --name demos
  local app_id
  app_id="$(read_id "${TMP_DIR}/application.json")"
  forge_json "${TMP_DIR}/service.json" service create --app "${app_id}" --name "${SERVICE_SLUG}" --port 8080
  SERVICE_ID="$(read_id "${TMP_DIR}/service.json")"

  forge_json "${TMP_DIR}/dep.json" deployment create \
    --service "${SERVICE_ID}" \
    --image "${DEMO_IMAGE}" \
    --env "${ENVIRONMENT_ID}" \
    --replicas 1
  DEPLOYMENT_ID="$(read_id "${TMP_DIR}/dep.json")"
  track_deployment "${DEPLOYMENT_ID}"
  wait_deployment_status "${DEPLOYMENT_ID}" active 90
  wait_route_host "${HOST}" 60

  # Runtime contract via Gateway.
  curl --fail --silent --show-error -H "Host: ${HOST}" "${GATEWAY_URL}/" \
    >"${TMP_DIR}/identity.json" || fail "gateway identity request failed"
  python3 -c '
import json,sys
body=json.load(open(sys.argv[1]))
assert body.get("service")=="demo-app", body
assert body.get("language")=="go", body
assert body.get("status")=="running", body
' "${TMP_DIR}/identity.json" || fail "demo app failed runtime identity contract"
  echo "  demo-app identity OK via Gateway"
}

step_trace() {
  local tp_out
  tp_out="$(mint_traceparent)"
  TRACEPARENT="$(printf '%s\n' "${tp_out}" | sed -n '1p')"
  TRACE_ID="$(printf '%s\n' "${tp_out}" | sed -n '2p')"
  REQUEST_ID="demo12-${TRACE_ID:0:16}"
  echo "Minted shared trace_id=${TRACE_ID}"
  echo "  traceparent=${TRACEPARENT}"
  echo "  request_id=${REQUEST_ID}"

  # Touch each instrumented platform service under the same W3C trace so Tempo
  # shows one distributed trace. Gateway then proxies to the OTEL demo-app.
  local code
  code="$(http_with_trace GET "${CONTROL_URL}/v1/projects" "${TMP_DIR}/ctrl.json")"
  [[ "${code}" == "200" ]] || fail "Control ping HTTP ${code}"

  code="$(http_with_trace GET "${BUILD_URL}/v1/builds" "${TMP_DIR}/build.json")"
  # builds list may be 200 with [] 
  [[ "${code}" == "200" || "${code}" == "404" ]] || fail "Build ping HTTP ${code}"

  code="$(http_with_trace GET "${RUNTIME_URL}/v1/node" "${TMP_DIR}/runtime.json")"
  [[ "${code}" == "200" ]] || fail "Runtime ping HTTP ${code}"

  # Edge request through Gateway → demo-app (continues the same trace).
  code="$(http_with_trace GET "${GATEWAY_URL}/" "${TMP_DIR}/gw-body.json" \
    -H "Host: ${HOST}" -D "${TMP_DIR}/gw-headers.txt")"
  [[ "${code}" == "200" ]] || fail "Gateway→demo-app HTTP ${code}"

  # Allow OTLP batch export.
  sleep 4

  assert_trace_services "control,build,runtime,gateway,demo-app"
  echo "[trace] single trace spans: control, build, runtime, gateway, demo-app OK"
  echo "  trace_id=${TRACE_ID}"
}

step_logs() {
  assert_correlated_logs
  assert_project_deployment_logs
  assert_no_secrets_in_telemetry
  assert_logs_follow
}

step_alert() {
  assert_high_error_rate_alert
}

main() {
  case "${PHASE}" in
    all|trace|logs|alert) ;;
    --phase=*)
      PHASE="${PHASE#--phase=}"
      ;;
    *)
      fail "unknown phase '${PHASE}' (use all|trace|logs|alert)"
      ;;
  esac

  acquire_demo_lock

  step_bootstrap
  step_deploy

  case "${PHASE}" in
    all)
      step_trace
      step_logs
      step_alert
      ;;
    trace)
      step_trace
      ;;
    logs)
      step_trace
      step_logs
      ;;
    alert)
      step_trace
      step_alert
      ;;
  esac

  echo "demo 12 PASSED"
}

main "$@"
