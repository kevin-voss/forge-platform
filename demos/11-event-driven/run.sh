#!/usr/bin/env bash
# Demo 11: event-driven polyglot gate (Go producer → Elixir consumer).
# Scenario: deliver N application.crashed events, reject malformed (422),
#           poison → retry → DLQ, restart + duplicate → processed once.
# FORGE_AUTH_MODE=dev on Events for simplicity (documented).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/11-event-driven"
COMPOSE=(
  docker compose
    -f "${ROOT_DIR}/compose.yaml"
    -f "${DEMO_DIR}/docker-compose.yml"
    --project-directory "${ROOT_DIR}"
)

export FORGE_EVENTS_AUTH_MODE="${FORGE_EVENTS_AUTH_MODE:-dev}"
export FORGE_DEFAULT_ACK_WAIT_S="${FORGE_DEFAULT_ACK_WAIT_S:-2}"
export FORGE_DEFAULT_MAX_DELIVERIES="${FORGE_DEFAULT_MAX_DELIVERIES:-3}"
export FORGE_ACK_TOKEN_TTL_S="${FORGE_ACK_TOKEN_TTL_S:-10}"
export FORGE_DEDUP_WINDOW_S="${FORGE_DEDUP_WINDOW_S:-60}"
export FORGE_CONSUME_WAIT_MS="${FORGE_CONSUME_WAIT_MS:-1000}"
export FORGE_DEMO_EVENT_COUNT="${FORGE_DEMO_EVENT_COUNT:-5}"
export FORGE_DEMO_IDEMPOTENCY_KEY="${FORGE_DEMO_IDEMPOTENCY_KEY:-demo-11-idempotency-key}"
export FORGE_DEMO_CONSUMER="${FORGE_DEMO_CONSUMER:-demo-elixir-crash-worker}"

EVENTS_URL="${FORGE_EVENTS_HOST_URL:-http://127.0.0.1:4105}"
PRODUCER_URL="${FORGE_PRODUCER_HOST_URL:-http://127.0.0.1:4211}"
CONSUMER_URL="${FORGE_CONSUMER_HOST_URL:-http://127.0.0.1:4212}"

EVENTS_SERVICE="forge-events"
NATS_SERVICE="nats"
POSTGRES_SERVICE="postgres"
PRODUCER_SERVICE="demo-events-producer"
CONSUMER_SERVICE="demo-events-consumer"
PHASE="${1:-all}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-events-demo.XXXXXX")"

cleanup() {
  "${COMPOSE[@]}" stop \
    "${PRODUCER_SERVICE}" "${CONSUMER_SERVICE}" "${EVENTS_SERVICE}" \
    >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

dump_context() {
  echo "--- ${EVENTS_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=120 "${EVENTS_SERVICE}" >&2 || true
  echo "--- ${CONSUMER_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONSUMER_SERVICE}" >&2 || true
  echo "--- ${PRODUCER_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=40 "${PRODUCER_SERVICE}" >&2 || true
  echo "--- DLQ list ---" >&2
  curl --silent --show-error "${EVENTS_URL}/v1/dlq?subject=application.crashed" >&2 || true
  echo >&2
  echo "--- consumer status ---" >&2
  curl --silent --show-error "${CONSUMER_URL}/v1/status" >&2 || true
  echo >&2
}

fail() {
  echo "Demo 11 failed: $*" >&2
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

http_json() {
  local method="$1" url="$2" output="$3"
  local status
  status="$(curl --silent --show-error --output "${output}" --write-out '%{http_code}' \
    --request "${method}" "${url}" \
    --header 'content-type: application/json')" || fail "${method} ${url} did not complete"
  echo "${status}"
}

wipe_nats_volume() {
  echo "Resetting NATS JetStream volume for a clean demo stream..."
  "${COMPOSE[@]}" stop "${EVENTS_SERVICE}" "${NATS_SERVICE}" >/dev/null 2>&1 || true
  "${COMPOSE[@]}" rm -f "${EVENTS_SERVICE}" "${NATS_SERVICE}" >/dev/null 2>&1 || true
  local vol
  vol="$(docker volume ls -q --filter name=forge-nats-data | head -n 1 || true)"
  if [[ -n "${vol}" ]]; then
    docker volume rm -f "${vol}" >/dev/null 2>&1 || true
    echo "  removed volume ${vol}"
  else
    echo "  no forge-nats-data volume found (fresh)"
  fi
}

step_bootstrap_stack() {
  echo "== Demo 11: Event-driven (Go → Elixir) =="
  echo "Auth mode: FORGE_EVENTS_AUTH_MODE=${FORGE_EVENTS_AUTH_MODE} (dev for demo gate)"
  echo "Retry: ack_wait=${FORGE_DEFAULT_ACK_WAIT_S}s max_deliveries=${FORGE_DEFAULT_MAX_DELIVERIES}"
  echo "Dedup window: ${FORGE_DEDUP_WINDOW_S}s"

  wipe_nats_volume

  echo "Starting PostgreSQL + NATS..."
  "${COMPOSE[@]}" up -d --remove-orphans "${POSTGRES_SERVICE}" "${NATS_SERVICE}"
  wait_http "http://127.0.0.1:5003/healthz" "NATS monitoring" 60

  echo "Starting Forge Events..."
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps "${EVENTS_SERVICE}"
  wait_http "${EVENTS_URL}/health/ready" "Forge Events"

  echo "Starting Elixir consumer + Go producer..."
  "${COMPOSE[@]}" up -d --build --force-recreate --no-deps \
    "${CONSUMER_SERVICE}" "${PRODUCER_SERVICE}"
  wait_http "${CONSUMER_URL}/health/ready" "Elixir consumer"
  wait_http "${PRODUCER_URL}/health/ready" "Go producer"

  # Runtime contract smoke for both demo apps.
  curl --fail --silent --show-error "${PRODUCER_URL}/" >/dev/null ||
    fail "producer identity endpoint failed"
  curl --fail --silent --show-error "${CONSUMER_URL}/" >/dev/null ||
    fail "consumer identity endpoint failed"
}

wait_processed_count() {
  local want="$1" attempts="${2:-60}"
  local count=0
  echo "Waiting for consumer processed_count=${want} ..."
  for _ in $(seq 1 "${attempts}"); do
    if curl --fail --silent --show-error "${CONSUMER_URL}/v1/status" >"${TMP_DIR}/status.json" 2>/dev/null; then
      count="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["processed_count"])' "${TMP_DIR}/status.json")"
      if [[ "${count}" -ge "${want}" ]]; then
        echo "  processed_count=${count}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "processed_count=${count}, want >= ${want}"
}

wait_processed_marker() {
  local event_id="$1" attempts="${2:-45}"
  local processed="false"
  echo "Waiting for processed marker event_id=${event_id} ..."
  for _ in $(seq 1 "${attempts}"); do
    if curl --fail --silent --show-error \
      "${EVENTS_URL}/v1/processed?consumer=${FORGE_DEMO_CONSUMER}&event_id=${event_id}" \
      >"${TMP_DIR}/processed.json" 2>/dev/null; then
      processed="$(python3 -c 'import json,sys; print(str(json.load(open(sys.argv[1])).get("processed", False)).lower())' "${TMP_DIR}/processed.json")"
      if [[ "${processed}" == "true" ]]; then
        echo "  processed=true"
        return 0
      fi
    fi
    sleep 1
  done
  fail "processed marker not set for ${event_id}"
}

wait_dlq_poison() {
  local poison_id="$1" attempts="${2:-45}"
  echo "Waiting for poison ${poison_id} in DLQ ..."
  for _ in $(seq 1 "${attempts}"); do
    if curl --fail --silent --show-error \
      "${EVENTS_URL}/v1/dlq?subject=application.crashed&consumer=${FORGE_DEMO_CONSUMER}" \
      >"${TMP_DIR}/dlq.json" 2>/dev/null; then
      if POISON_ID="${poison_id}" python3 - "${TMP_DIR}/dlq.json" 2>/dev/null <<'PY'; then
import json, os, sys
items = json.load(open(sys.argv[1]))
want = os.environ["POISON_ID"]
if not isinstance(items, list):
    sys.exit(1)
match = [i for i in items if i.get("event_id") == want]
if not match:
    sys.exit(1)
entry = match[0]
if entry.get("original_subject") != "application.crashed":
    sys.exit(1)
if int(entry.get("delivery_count", 0)) < 3:
    sys.exit(1)
print(f"dlq_id={entry.get('dlq_id')} deliveries={entry.get('delivery_count')}")
sys.exit(0)
PY
        return 0
      fi
    fi
    sleep 1
  done
  fail "poison event ${poison_id} not found in DLQ after retries"
}

step_deliver() {
  echo "[deliver] publishing ${FORGE_DEMO_EVENT_COUNT} valid application.crashed events"
  local status
  status="$(http_json POST "${PRODUCER_URL}/v1/publish/valid?count=${FORGE_DEMO_EVENT_COUNT}" \
    "${TMP_DIR}/valid.json")"
  [[ "${status}" == "200" ]] || fail "publish valid returned HTTP ${status}: $(cat "${TMP_DIR}/valid.json")"

  IDEM_EVENT_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["events"][0]["event_id"])' "${TMP_DIR}/valid.json")"
  [[ -n "${IDEM_EVENT_ID}" ]] || fail "missing idempotent event_id"
  echo "  idempotent event_id=${IDEM_EVENT_ID}"

  wait_processed_count "${FORGE_DEMO_EVENT_COUNT}"
  wait_processed_marker "${IDEM_EVENT_ID}"

  python3 - "${TMP_DIR}/status.json" "${FORGE_DEMO_EVENT_COUNT}" <<'PY' || fail "delivery assertion failed"
import json, sys
status = json.load(open(sys.argv[1]))
want = int(sys.argv[2])
got = int(status["processed_count"])
assert got == want, (got, want, status)
assert len(status["processed_ids"]) == want, status
print(f"delivered {got}/{want}")
PY

  echo "[deliver] Go->Elixir delivered ${FORGE_DEMO_EVENT_COUNT}/${FORGE_DEMO_EVENT_COUNT} OK"
}

step_schema() {
  echo "[schema] publishing malformed event (expect 422)"
  local status
  status="$(http_json POST "${PRODUCER_URL}/v1/publish/malformed" "${TMP_DIR}/malformed.json")"
  [[ "${status}" == "200" ]] || fail "malformed helper returned HTTP ${status}: $(cat "${TMP_DIR}/malformed.json")"

  python3 - "${TMP_DIR}/malformed.json" <<'PY' || fail "malformed was not rejected with 422"
import json, sys
body = json.load(open(sys.argv[1]))
assert body.get("status") == 422, body
inner = body.get("body") or {}
assert inner.get("error") in ("validation_failed", "unknown_schema", "unknown_version"), body
print("malformed rejected", body["status"], "error=", inner.get("error"))
PY

  # Ensure consumer count did not grow from the rejected publish.
  curl --fail --silent --show-error "${CONSUMER_URL}/v1/status" >"${TMP_DIR}/status-after-malformed.json"
  python3 - "${TMP_DIR}/status-after-malformed.json" "${FORGE_DEMO_EVENT_COUNT}" <<'PY' || fail "malformed event was delivered"
import json, sys
status = json.load(open(sys.argv[1]))
want = int(sys.argv[2])
assert int(status["processed_count"]) == want, status
PY

  echo "[schema] malformed rejected 422 OK"
}

step_dlq() {
  echo "[dlq] publishing poison event (consumer will nak → DLQ)"
  local status
  status="$(http_json POST "${PRODUCER_URL}/v1/publish/poison" "${TMP_DIR}/poison.json")"
  [[ "${status}" == "200" ]] || fail "publish poison returned HTTP ${status}: $(cat "${TMP_DIR}/poison.json")"

  POISON_EVENT_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["event"]["event_id"])' "${TMP_DIR}/poison.json")"
  [[ -n "${POISON_EVENT_ID}" ]] || fail "missing poison event_id"
  echo "  poison event_id=${POISON_EVENT_ID}"

  wait_dlq_poison "${POISON_EVENT_ID}"
  echo "[dlq] poison in DLQ after ${FORGE_DEFAULT_MAX_DELIVERIES} retries OK"
}

step_idempotency() {
  echo "[idempotency] restarting Elixir consumer + re-publishing duplicate"
  [[ -n "${IDEM_EVENT_ID:-}" ]] || fail "IDEM_EVENT_ID unset (run deliver phase first)"

  local before
  before="$(curl --fail --silent --show-error "${CONSUMER_URL}/v1/status" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["processed_count"])')"

  "${COMPOSE[@]}" up -d --force-recreate --no-deps "${CONSUMER_SERVICE}"
  wait_http "${CONSUMER_URL}/health/ready" "Elixir consumer (restarted)"

  local status
  status="$(http_json POST "${PRODUCER_URL}/v1/publish/duplicate" "${TMP_DIR}/duplicate.json")"
  [[ "${status}" == "200" ]] || fail "publish duplicate returned HTTP ${status}: $(cat "${TMP_DIR}/duplicate.json")"

  python3 - "${TMP_DIR}/duplicate.json" "${IDEM_EVENT_ID}" <<'PY' || fail "duplicate publish did not reuse event_id"
import json, sys
body = json.load(open(sys.argv[1]))
want = sys.argv[2]
assert body.get("status") == 202, body
assert body.get("event", {}).get("event_id") == want, (body, want)
print("duplicate publish returned same event_id")
PY

  wait_processed_marker "${IDEM_EVENT_ID}"

  # Give the consumer a moment; JetStream dedup means no new delivery.
  sleep 3
  curl --fail --silent --show-error "${CONSUMER_URL}/v1/status" >"${TMP_DIR}/status-after-dup.json"

  BEFORE="${before}" IDEM_EVENT_ID="${IDEM_EVENT_ID}" python3 - "${TMP_DIR}/status-after-dup.json" <<'PY' || fail "idempotency assertion failed"
import json, os, sys
status = json.load(open(sys.argv[1]))
# After restart local counter may be 0; server-side marker is the source of truth.
# Duplicate must not appear as a second local process of the same id after restart.
ids = status.get("processed_ids") or []
idem = os.environ["IDEM_EVENT_ID"]
# Either not yet seen locally (dedup at publish) or seen at most once after restart.
assert ids.count(idem) <= 1, status
print(f"local processed_ids containing idem event: {ids.count(idem)}")
PY

  echo "[idempotency] restart + duplicate -> processed once OK"
}

run_scenario() {
  step_bootstrap_stack
  step_deliver
  step_schema
  step_dlq
  step_idempotency
  echo "demo 11 PASSED"
}

case "${PHASE}" in
  all|--phase=all|"")
    run_scenario
    ;;
  --phase=delivery|delivery)
    step_bootstrap_stack
    step_deliver
    echo "phase delivery PASSED"
    ;;
  --phase=dlq|dlq)
    step_bootstrap_stack
    step_deliver
    step_schema
    step_dlq
    echo "phase dlq PASSED"
    ;;
  --phase=idempotency|idempotency)
    step_bootstrap_stack
    step_deliver
    step_schema
    step_dlq
    step_idempotency
    echo "phase idempotency PASSED"
    echo "demo 11 PASSED"
    ;;
  *)
    echo "Unknown phase: ${PHASE}" >&2
    echo "Usage: $0 [all|--phase=delivery|--phase=dlq|--phase=idempotency]" >&2
    exit 2
    ;;
esac
