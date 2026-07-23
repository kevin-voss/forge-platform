#!/usr/bin/env bash
# Acceptance assertions for demo 18 (sourced or called from run.sh).
# Expects environment variables set by run.sh.
set -euo pipefail

: "${FORGE_BIN:?}"
: "${CONTROL_URL:?}"
: "${PROJECT_ID:?}"
: "${APPLICATION_ID:?}"
: "${SERVICE_ID:?}"
: "${ENVIRONMENT_ID:?}"
: "${DEMO_IMAGE:?}"
: "${TMP_DIR:?}"
: "${SESSION_TOKEN:?}"
: "${RUNTIME_URL:?}"
: "${FIXTURE_KEY:=demo18-fixture}"
: "${FIXTURE_VALUE:=managed-db-ok}"
: "${SERVICE_SLUG:=backend}"
: "${DB_NAME:=main}"

fail() {
  echo "acceptance failed: $*" >&2
  exit 1
}

forge() {
  echo "+ forge $*" >&2
  "${FORGE_BIN}" "$@"
}

forge_json() {
  local output="$1"
  shift
  forge --output json "$@" >"${output}" || fail "forge $* failed"
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${output}" ||
    fail "forge $* did not emit JSON: $(cat "${output}")"
}

read_id() {
  python3 -c 'import json,sys,uuid; value=json.load(open(sys.argv[1]))["id"]; uuid.UUID(value); print(value)' "$1" ||
    fail "missing UUID id in $1: $(cat "$1")"
}

deployment_short() {
  python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "$1"
}

wait_deployment_status() {
  local deployment_id="$1" expected="$2" attempts="${3:-90}"
  local status=""
  echo "Waiting for deployment ${deployment_id} status=${expected} ..."
  for _ in $(seq 1 "${attempts}"); do
    forge_json "${TMP_DIR}/dep-status.json" deployment status "${deployment_id}"
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "${TMP_DIR}/dep-status.json")"
    if [[ "${status}" == "${expected}" ]] ||
      { [[ "${expected}" == "active" || "${expected}" == "deployed" ]] &&
        [[ "${status}" == "active" || "${status}" == "deployed" ]]; }; then
      echo "  status=${status}"
      return 0
    fi
    sleep 2
  done
  fail "deployment ${deployment_id} status=${status:-unknown}, want ${expected}"
}

wait_container() {
  local deployment_id="$1" attempts="${2:-90}"
  local short i
  short="$(deployment_short "${deployment_id}")"
  echo "Waiting for managed container matching ${SERVICE_SLUG}-${short}-* (up to $((attempts * 2))s) ..."
  for ((i = 1; i <= attempts; i++)); do
    if docker ps --filter "label=forge.managed=true" \
      --filter "name=forge-${SERVICE_SLUG}-${short}-" --format '{{.Names}}' 2>/dev/null |
      grep -q "forge-${SERVICE_SLUG}-${short}-"; then
      echo "  container present (attempt ${i})"
      return 0
    fi
    if ((i % 15 == 0)); then
      echo "  still waiting (attempt ${i}/${attempts}) ..."
    fi
    sleep 2 || true
  done
  echo "--- docker ps (forge.managed) ---" >&2
  docker ps --filter "label=forge.managed=true" --format '{{.Names}} {{.Status}}' >&2 || true
  fail "container for deployment ${deployment_id} (short=${short}) did not appear"
}

host_port_for() {
  local deployment_id="$1"
  local short
  short="$(deployment_short "${deployment_id}")"
  curl --fail --silent --show-error "${RUNTIME_URL}/v1/node/state" |
    DEPLOYMENT_SHORT="${short}" python3 -c '
import json, os, sys
state = json.load(sys.stdin)
short = os.environ["DEPLOYMENT_SHORT"]
for w in state.get("workloads", []):
    rid = w.get("deploymentId") or ""
    if short in rid and w.get("hostPort"):
        print(w["hostPort"])
        sys.exit(0)
sys.exit("hostPort not found for short id " + short)
' || fail "could not read hostPort for ${deployment_id}"
}

wait_http_json() {
  local url="$1" label="$2" attempts="${3:-60}"
  local body=""
  echo "Waiting for ${label} at ${url} ..." >&2
  for _ in $(seq 1 "${attempts}"); do
    if body="$(curl --fail --silent --show-error "${url}" 2>/dev/null)"; then
      echo "  ${label} OK" >&2
      printf '%s' "${body}"
      return 0
    fi
    sleep 2
  done
  fail "timed out waiting for ${label}"
}

wait_backup_succeeded() {
  local database_id="$1" backup_id="$2" attempts="${3:-60}"
  local status=""
  echo "Waiting for backup ${backup_id} succeeded ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error \
      -H "Authorization: Bearer ${SESSION_TOKEN}" \
      -H "X-Forge-Project: ${PROJECT_ID}" \
      "${CONTROL_URL}/v1/databases/${database_id}/backups/${backup_id}" \
      >"${TMP_DIR}/backup-status.json" || true
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("status",""))' "${TMP_DIR}/backup-status.json" 2>/dev/null || true)"
    if [[ "${status}" == "succeeded" ]]; then
      echo "  backup succeeded"
      return 0
    fi
    if [[ "${status}" == "failed" ]]; then
      fail "backup failed: $(cat "${TMP_DIR}/backup-status.json")"
    fi
    sleep 2
  done
  fail "backup ${backup_id} did not succeed (last=${status:-unknown})"
}

wait_restore_succeeded() {
  local database_id="$1" backup_id="$2" attempts="${3:-60}"
  local status=""
  echo "Waiting for restore of backup ${backup_id} succeeded ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --fail --silent --show-error \
      -H "Authorization: Bearer ${SESSION_TOKEN}" \
      -H "X-Forge-Project: ${PROJECT_ID}" \
      "${CONTROL_URL}/v1/databases/${database_id}/backups/${backup_id}" \
      >"${TMP_DIR}/restore-status.json" || true
    status="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("restoreStatus",""))' "${TMP_DIR}/restore-status.json" 2>/dev/null || true)"
    if [[ "${status}" == "succeeded" ]]; then
      echo "  restore succeeded"
      return 0
    fi
    if [[ "${status}" == "failed" ]]; then
      fail "restore failed: $(cat "${TMP_DIR}/restore-status.json")"
    fi
    sleep 2
  done
  fail "restore did not succeed (last=${status:-unknown})"
}

assert_no_hardcoded_creds_in_app() {
  local app_dir="$1"
  python3 - "$app_dir" <<'PY' || fail "hardcoded credentials found in demo app source"
import pathlib, re, sys
# Production app entrypoint only — unit tests may use placeholder URLs.
path = pathlib.Path(sys.argv[1]) / "server.py"
text = path.read_text(errors="replace")
patterns = [
    re.compile(r"postgresql://[^\"'\s]+:[^\"'\s]+@"),
    re.compile(r"password\s*=\s*['\"][^'\"]+['\"]", re.I),
    re.compile(r"DATABASE_URL\s*=\s*['\"]postgresql:", re.I),
]
leaks = []
for pat in patterns:
    for match in pat.finditer(text):
        snippet = text[max(0, match.start() - 40): match.end() + 40]
        # Isolation guard mentions Control's JDBC URL shape without embedding creds.
        if "refusing" in snippet or "postgres:5432/forge" in snippet:
            continue
        leaks.append(f"{path.name}: {pat.pattern}")
if leaks:
    raise SystemExit("leaks: " + "; ".join(leaks))
print("app source has no hardcoded credentials: OK")
PY
}

echo "== acceptance: forge database lifecycle =="

assert_no_hardcoded_creds_in_app "${APP_DIR:?}"

echo "[create] forge database create ${DB_NAME}"
forge_json "${TMP_DIR}/db-create.json" --project "${PROJECT_ID}" database create "${DB_NAME}"
DATABASE_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["database"]["id"])' "${TMP_DIR}/db-create.json")"
INSTANCE_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["instance"]["id"])' "${TMP_DIR}/db-create.json")"
INSTANCE_HOST="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["instance"].get("host") or "")' "${TMP_DIR}/db-create.json")"
[[ -n "${DATABASE_ID}" ]] || fail "database id missing"
echo "  instance=${INSTANCE_ID} database=${DATABASE_ID} host=${INSTANCE_HOST}"
if [[ "${INSTANCE_HOST}" == *"postgres"* && "${INSTANCE_HOST}" != *"host.docker.internal"* ]]; then
  # host should not be the Control postgres service name
  [[ "${INSTANCE_HOST}" != "postgres" ]] || fail "product DB host must not be Control postgres"
fi

echo "[list] forge database list"
forge_json "${TMP_DIR}/db-list.json" --project "${PROJECT_ID}" database list
python3 - "${TMP_DIR}/db-list.json" "${DATABASE_ID}" <<'PY' || fail "list missing created database"
import json, sys
rows, want = json.load(open(sys.argv[1])), sys.argv[2]
assert any(r.get("id") == want for r in rows), rows
print("list contains database", want)
PY

echo "[attach] forge database attach ${DB_NAME} --app backend"
forge_json "${TMP_DIR}/db-attach.json" --project "${PROJECT_ID}" \
  database attach "${DB_NAME}" --app "${APPLICATION_NAME:-backend}" --env-var DATABASE_URL
ATTACHMENT_ID="$(read_id "${TMP_DIR}/db-attach.json")"
SECRET_REF="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("secretRef") or "")' "${TMP_DIR}/db-attach.json")"
[[ -n "${SECRET_REF}" ]] || fail "attach missing secretRef"
[[ "${SECRET_REF}" != *"://"* ]] || fail "attach secretRef looks like a plaintext URL"
echo "  attachment=${ATTACHMENT_ID} secretRef=${SECRET_REF}"

echo "[deploy] forge deployment create (injected DATABASE_URL)"
forge_json "${TMP_DIR}/deployment.json" deployment create \
  --service "${SERVICE_ID}" \
  --image "${DEMO_IMAGE}" \
  --env "${ENVIRONMENT_ID}" \
  --replicas 1
DEPLOYMENT_ID="$(read_id "${TMP_DIR}/deployment.json")"
export DEPLOYMENT_ID
wait_container "${DEPLOYMENT_ID}"
wait_deployment_status "${DEPLOYMENT_ID}" "active"
HOST_PORT="$(host_port_for "${DEPLOYMENT_ID}")"
echo "  deployment=${DEPLOYMENT_ID} hostPort=${HOST_PORT}"

body="$(wait_http_json "http://127.0.0.1:${HOST_PORT}/health/ready" "app ready")"
body="$(wait_http_json "http://127.0.0.1:${HOST_PORT}/db-status" "db-status")"
python3 -c '
import json,sys
p=json.load(sys.stdin)
assert p.get("DATABASE_URL_present") is True, p
assert p.get("fixture_value") == sys.argv[1], p
assert "postgresql://" not in json.dumps(p)
print("db-status fixture OK; URL not echoed")
' "${FIXTURE_VALUE}" <<<"${body}" || fail "db-status assertion failed"

fixture="$(curl --fail --silent --show-error "http://127.0.0.1:${HOST_PORT}/fixture")"
python3 -c '
import json,sys
p=json.load(sys.stdin)
assert p.get("key")==sys.argv[1] and p.get("value")==sys.argv[2], p
print("fixture read OK")
' "${FIXTURE_KEY}" "${FIXTURE_VALUE}" <<<"${fixture}" || fail "fixture read failed"

echo "[backup] forge database backup ${DB_NAME}"
forge_json "${TMP_DIR}/db-backup.json" --project "${PROJECT_ID}" database backup "${DB_NAME}"
BACKUP_ID="$(read_id "${TMP_DIR}/db-backup.json")"
wait_backup_succeeded "${DATABASE_ID}" "${BACKUP_ID}"
CHECKSUM="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("checksum") or "")' "${TMP_DIR}/backup-status.json")"
[[ -n "${CHECKSUM}" ]] || fail "backup missing checksum"
echo "  backup=${BACKUP_ID} checksum=${CHECKSUM}"

echo "[clear] wipe fixture to prove restore"
curl --fail --silent --show-error -X POST "http://127.0.0.1:${HOST_PORT}/fixture/clear" >/dev/null
cleared="$(curl --fail --silent --show-error "http://127.0.0.1:${HOST_PORT}/fixture")"
python3 -c '
import json,sys
p=json.load(sys.stdin)
assert p.get("value") in (None, ""), p
print("fixture cleared OK")
' <<<"${cleared}" || fail "fixture clear failed"

echo "[restore] forge database restore ${BACKUP_ID} --target ${DB_NAME}"
forge_json "${TMP_DIR}/db-restore.json" --project "${PROJECT_ID}" \
  database restore "${BACKUP_ID}" --target "${DB_NAME}"
wait_restore_succeeded "${DATABASE_ID}" "${BACKUP_ID}"

restored="$(curl --fail --silent --show-error "http://127.0.0.1:${HOST_PORT}/fixture")"
python3 -c '
import json,sys
p=json.load(sys.stdin)
assert p.get("value")==sys.argv[1], p
print("fixture recovered after restore OK")
' "${FIXTURE_VALUE}" <<<"${restored}" || fail "fixture not recovered after restore"

echo "[rotate] forge database rotate ${DB_NAME} (referenced from 18.05)"
forge_json "${TMP_DIR}/db-rotate.json" --project "${PROJECT_ID}" database rotate "${DB_NAME}"
ROT_USER="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["credential"]["username"])' "${TMP_DIR}/db-rotate.json")"
[[ -n "${ROT_USER}" ]] || fail "rotate missing username"
echo "  rotated username=${ROT_USER}"

echo "acceptance PASSED"
