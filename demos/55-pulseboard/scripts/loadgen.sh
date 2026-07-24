#!/usr/bin/env bash
# PulseBoard HTTP load generator (epic 55.02).
#
# Starts/stops sustained load against api.pulseboard.localhost and publishes
# matching requestsPerSecond to the demo55-metrics sidecar so forge-autoscaler
# can evaluate the httpRequests ScalingPolicy (Gateway does not yet expose
# /admin/metrics; same pattern as demos 24/52).
#
# Usage:
#   demos/55-pulseboard/scripts/loadgen.sh start [--rps N]
#   demos/55-pulseboard/scripts/loadgen.sh stop
#   demos/55-pulseboard/scripts/loadgen.sh status
# Env:
#   GATEWAY_URL     default http://127.0.0.1:4000
#   API_HOST        default api.pulseboard.localhost
#   METRICS_URL     default http://127.0.0.1:4197
#   APPLICATION     default pulseboard-api
#   LOADGEN_RPS     default 250
#   LOADGEN_PID_FILE default demos/55-pulseboard/.loadgen.pid
#   PUBLISH_METRICS default 1 (set 0 to skip metrics PUT)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:4000}"
API_HOST="${API_HOST:-api.pulseboard.localhost}"
METRICS_URL="${METRICS_URL:-http://127.0.0.1:4197}"
APPLICATION="${APPLICATION:-pulseboard-api}"
LOADGEN_RPS="${LOADGEN_RPS:-250}"
LOADGEN_PID_FILE="${LOADGEN_PID_FILE:-${DEMO_DIR}/.loadgen.pid}"
PUBLISH_METRICS="${PUBLISH_METRICS:-1}"
HIT_PATH="${HIT_PATH:-/hit}"

usage() {
  sed -n '2,20p' "$0"
}

publish_rps() {
  local rps="$1"
  [[ "${PUBLISH_METRICS}" == "1" ]] || return 0
  curl --fail --silent --show-error -X PUT \
    "${METRICS_URL}/demo/application/${APPLICATION}" \
    -H 'content-type: application/json' \
    -d "{\"requestsPerSecond\":${rps},\"activeConnections\":${rps},\"sampleCount\":2000}" \
    >/dev/null
}

clear_metrics() {
  [[ "${PUBLISH_METRICS}" == "1" ]] || return 0
  curl --silent --show-error -X DELETE \
    "${METRICS_URL}/demo/application/${APPLICATION}" >/dev/null 2>&1 || true
}

is_running() {
  [[ -f "${LOADGEN_PID_FILE}" ]] || return 1
  local pid
  pid="$(cat "${LOADGEN_PID_FILE}" 2>/dev/null || true)"
  [[ -n "${pid}" ]] || return 1
  kill -0 "${pid}" 2>/dev/null
}

stop_load() {
  if [[ -f "${LOADGEN_PID_FILE}" ]]; then
    local pid
    pid="$(cat "${LOADGEN_PID_FILE}" 2>/dev/null || true)"
    if [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" >/dev/null 2>&1 || true
      wait "${pid}" 2>/dev/null || true
      # Also stop children of the python process group if any.
      pkill -P "${pid}" >/dev/null 2>&1 || true
    fi
    rm -f "${LOADGEN_PID_FILE}"
  fi
  clear_metrics
  echo "loadgen: stopped application=${APPLICATION}"
}

start_load() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --rps|-r) LOADGEN_RPS="$2"; shift 2 ;;
      --no-metrics) PUBLISH_METRICS=0; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        echo "Unknown arg: $1" >&2
        exit 2
        ;;
    esac
  done

  if is_running; then
    echo "loadgen: already running pid=$(cat "${LOADGEN_PID_FILE}") rps=${LOADGEN_RPS}"
    publish_rps "${LOADGEN_RPS}"
    return 0
  fi

  publish_rps "${LOADGEN_RPS}"

  # Background worker: refresh metrics + generate real Gateway traffic.
  GATEWAY_URL="${GATEWAY_URL}" API_HOST="${API_HOST}" METRICS_URL="${METRICS_URL}" \
  APPLICATION="${APPLICATION}" LOADGEN_RPS="${LOADGEN_RPS}" HIT_PATH="${HIT_PATH}" \
  PUBLISH_METRICS="${PUBLISH_METRICS}" python3 - <<'PY' &
import json, os, time, urllib.error, urllib.request

gateway = os.environ["GATEWAY_URL"].rstrip("/")
host = os.environ["API_HOST"]
metrics = os.environ["METRICS_URL"].rstrip("/")
app = os.environ["APPLICATION"]
rps = float(os.environ["LOADGEN_RPS"])
path = os.environ.get("HIT_PATH", "/hit")
publish = os.environ.get("PUBLISH_METRICS", "1") == "1"

hit_url = f"{gateway}{path}"
metrics_url = f"{metrics}/demo/application/{app}"
interval = 1.0 / max(rps, 1.0)
# Cap concurrent-ish burst size per tick so we don't open thousands of sockets.
batch = max(1, min(int(rps), 50))
tick = batch * interval

def put_metrics():
    if not publish:
        return
    body = json.dumps({
        "requestsPerSecond": rps,
        "activeConnections": rps,
        "sampleCount": 2000,
    }).encode()
    req = urllib.request.Request(
        metrics_url, data=body, method="PUT",
        headers={"content-type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=3) as resp:
            resp.read()
    except Exception as exc:
        print(f"loadgen metrics: {exc}", flush=True)

def hit_once():
    req = urllib.request.Request(
        hit_url, data=b"{}", method="POST",
        headers={"Host": host, "content-type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=2) as resp:
            resp.read()
    except Exception:
        pass

print(f"loadgen: running host={host} rps={rps} path={path}", flush=True)
next_metrics = 0.0
while True:
    now = time.monotonic()
    if now >= next_metrics:
        put_metrics()
        next_metrics = now + 1.0
    for _ in range(batch):
        hit_once()
    # Pace approximately to target RPS (best-effort; metrics sidecar is authoritative).
    elapsed = time.monotonic() - now
    sleep_for = tick - elapsed
    if sleep_for > 0:
        time.sleep(sleep_for)
PY
  echo $! >"${LOADGEN_PID_FILE}"
  echo "loadgen: started pid=$(cat "${LOADGEN_PID_FILE}") application=${APPLICATION} rps=${LOADGEN_RPS} host=${API_HOST}"
}

status_load() {
  if is_running; then
    echo "loadgen: running pid=$(cat "${LOADGEN_PID_FILE}")"
    if [[ "${PUBLISH_METRICS}" == "1" ]]; then
      curl --fail --silent --show-error \
        "${METRICS_URL}/admin/metrics?application=${APPLICATION}" || true
      echo
    fi
    return 0
  fi
  echo "loadgen: stopped"
  return 1
}

cmd="${1:-}"
shift || true
case "${cmd}" in
  start) start_load "$@" ;;
  stop) stop_load ;;
  status) status_load ;;
  -h|--help|"") usage; [[ -n "${cmd}" ]] || exit 2; exit 0 ;;
  *)
    echo "Usage: $0 {start|stop|status} [--rps N]" >&2
    exit 2
    ;;
esac
