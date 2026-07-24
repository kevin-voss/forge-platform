#!/usr/bin/env bash
# SnapNote burst helper (epic 52.04).
# Enqueues N attachment.uploaded messages via the API (presign → PUT → complete)
# and optionally publishes matching queueDepth to the demo metrics sidecar so
# forge-autoscaler can evaluate the ScalingPolicy.
#
# Usage:
#   demos/52-snapnote/scripts/burst.sh [--count N] [--depth D] [--note-id ID]
# Env:
#   GATEWAY_URL   default http://127.0.0.1:4000
#   API_HOST      default api.snapnote.localhost
#   STORAGE_URL   default http://127.0.0.1:4107
#   METRICS_URL   default http://127.0.0.1:4198 (demo52-metrics)
#   QUEUE_NAME    default snapnote-attachments
#   PUBLISH_METRICS  default 1 (set 0 to skip metrics PUT)
set -euo pipefail

COUNT="${BURST_COUNT:-40}"
DEPTH=""
NOTE_ID=""
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:4000}"
API_HOST="${API_HOST:-api.snapnote.localhost}"
STORAGE_URL="${STORAGE_URL:-http://127.0.0.1:4107}"
METRICS_URL="${METRICS_URL:-http://127.0.0.1:4198}"
QUEUE_NAME="${QUEUE_NAME:-snapnote-attachments}"
PUBLISH_METRICS="${PUBLISH_METRICS:-1}"
RETRY_RATE="${RETRY_RATE:-0}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --count|-n) COUNT="$2"; shift 2 ;;
    --depth|-d) DEPTH="$2"; shift 2 ;;
    --note-id) NOTE_ID="$2"; shift 2 ;;
    --retry-rate) RETRY_RATE="$2"; shift 2 ;;
    --no-metrics) PUBLISH_METRICS=0; shift ;;
    -h|--help)
      sed -n '2,18p' "$0"
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "${DEPTH}" ]]; then
  DEPTH="${COUNT}"
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/snapnote-burst.XXXXXX")"
cleanup() { rm -rf "${TMP_DIR}"; }
trap cleanup EXIT

rewrite_storage_url() {
  local url="$1"
  UPLOAD_URL="${url}" STORAGE_URL="${STORAGE_URL}" python3 - <<'PY'
import os, urllib.parse
u = os.environ["UPLOAD_URL"]
storage = os.environ["STORAGE_URL"].rstrip("/")
parsed = urllib.parse.urlparse(u)
if parsed.path.startswith("/storage/"):
    path = parsed.path[len("/storage"):]
    print(storage + path + (("?" + parsed.query) if parsed.query else ""))
else:
    print(u)
PY
}

publish_queue_metrics() {
  local depth="$1" retry="${2:-0}"
  [[ "${PUBLISH_METRICS}" == "1" ]] || return 0
  curl --fail --silent --show-error -X PUT "${METRICS_URL}/demo/queue/${QUEUE_NAME}" \
    -H 'content-type: application/json' \
    -d "{\"depth\":${depth},\"oldestAgeSeconds\":15,\"consumerLag\":${depth},\"retryRate\":${retry}}" \
    >/dev/null
  echo "  metrics: queue=${QUEUE_NAME} depth=${depth} retryRate=${retry}"
}

if [[ -z "${NOTE_ID}" ]]; then
  title="burst-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"body":"burst upload"}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/note.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "${body}" "${GATEWAY_URL}/notes" || echo "000")"
  [[ "${code}" == "201" ]] || {
    echo "create note HTTP ${code}: $(cat "${TMP_DIR}/note.json" 2>/dev/null || true)" >&2
    exit 1
  }
  NOTE_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/note.json")"
  echo "Created note ${NOTE_ID} (${title})"
else
  echo "Using note ${NOTE_ID}"
fi

# Publish backlog signal before uploads so the autoscaler can react while
# messages are still enqueued / in flight.
publish_queue_metrics "${DEPTH}" "${RETRY_RATE}"

echo "Enqueuing ${COUNT} attachments on note ${NOTE_ID}..."
ATT_IDS=()
for i in $(seq 1 "${COUNT}"); do
  code="$(curl --silent --show-error -o "${TMP_DIR}/att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "{\"filename\":\"burst-${i}.jpg\",\"contentType\":\"image/jpeg\"}" \
    "${GATEWAY_URL}/notes/${NOTE_ID}/attachments" || echo "000")"
  [[ "${code}" == "201" ]] || {
    echo "create attachment ${i} HTTP ${code}: $(cat "${TMP_DIR}/att.json" 2>/dev/null || true)" >&2
    exit 1
  }
  att_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["id"])' "${TMP_DIR}/att.json")"
  upload_url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["uploadUrl"])' "${TMP_DIR}/att.json")"
  ATT_IDS+=("${att_id}")

  payload="${TMP_DIR}/burst-${i}.bin"
  printf 'snapnote-burst-%s-%s' "${i}" "${att_id}" >"${payload}"
  put_url="$(rewrite_storage_url "${upload_url}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/put.json" -w '%{http_code}' \
    -X PUT -H 'content-type: image/jpeg' --data-binary @"${payload}" \
    "${put_url}" || echo "000")"
  [[ "${code}" == "201" || "${code}" == "200" ]] || {
    echo "PUT attachment ${i} HTTP ${code}" >&2
    exit 1
  }

  code="$(curl --silent --show-error -o "${TMP_DIR}/complete.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -X POST \
    "${GATEWAY_URL}/notes/${NOTE_ID}/attachments/${att_id}/complete" || echo "000")"
  [[ "${code}" == "202" || "${code}" == "200" ]] || {
    echo "complete attachment ${i} HTTP ${code}: $(cat "${TMP_DIR}/complete.json" 2>/dev/null || true)" >&2
    exit 1
  }
done

# Refresh depth to remaining backlog estimate after enqueue.
publish_queue_metrics "${DEPTH}" "${RETRY_RATE}"

echo "BURST_NOTE_ID=${NOTE_ID}"
echo "BURST_COUNT=${COUNT}"
echo "BURST_DEPTH=${DEPTH}"
echo "BURST_ATTACHMENT_IDS=${ATT_IDS[*]}"
echo "Burst enqueue complete (${COUNT} messages → queue ${QUEUE_NAME})."
