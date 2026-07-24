#!/usr/bin/env bash
# Demo 52: SnapNote + managed Postgres + object storage (epic 52.02).
# Usage:
#   demos/52-snapnote/run.sh          # build → apply → DB → storage → Ready → seed → proofs
#   demos/52-snapnote/run.sh --down   # tear down product resources only
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO_DIR="${ROOT_DIR}/demos/52-snapnote"
STATE_FILE="${DEMO_DIR}/.demo-state"

export FORGE_AUTH_MODE="${FORGE_AUTH_MODE:-dev}"
export FORGE_LIFECYCLE_OWNER="${FORGE_LIFECYCLE_OWNER:-control}"
export FORGE_RECONCILE_INTERVAL_MS="${FORGE_RECONCILE_INTERVAL_MS:-1000}"
export FORGE_READINESS_POLL_MS="${FORGE_READINESS_POLL_MS:-500}"
export FORGE_READINESS_MAX_WAIT_S="${FORGE_READINESS_MAX_WAIT_S:-90}"
export FORGE_RESOURCE_API_ENABLED="${FORGE_RESOURCE_API_ENABLED:-true}"
export FORGE_SECRETS_URL="${FORGE_SECRETS_URL:-disabled}"
export FORGE_OTEL_ENABLED="${FORGE_OTEL_ENABLED:-false}"
export FORGE_SCHEDULER_STRATEGY="${FORGE_SCHEDULER_STRATEGY:-single-node}"
export FORGE_SCHEDULER_LOCAL_NODE_ID="${FORGE_SCHEDULER_LOCAL_NODE_ID:-node-local}"
export FORGE_ROUTE_SYNC_INTERVAL_SECONDS="${FORGE_ROUTE_SYNC_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS="${FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS:-2}"
export FORGE_UPSTREAM_FAILURE_THRESHOLD="${FORGE_UPSTREAM_FAILURE_THRESHOLD:-1}"
export FORGE_UPSTREAM_SUCCESS_THRESHOLD="${FORGE_UPSTREAM_SUCCESS_THRESHOLD:-1}"
export FORGE_DB_PROVISIONER="${FORGE_DB_PROVISIONER:-local}"
export FORGE_DB_ENDPOINT_HOST="${FORGE_DB_ENDPOINT_HOST:-host.docker.internal}"
export FORGE_DB_MANAGED_NETWORK="${FORGE_DB_MANAGED_NETWORK:-forge-net}"
export FORGE_INJECT_MASK_IN_LOGS="${FORGE_INJECT_MASK_IN_LOGS:-true}"
export DOCKER_GID="${DOCKER_GID:-$(stat -f '%g' /var/run/docker.sock 2>/dev/null || stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 0)}"
export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-1}"

COMPOSE=(
  docker compose
  -f "${ROOT_DIR}/compose.yaml"
  -f "${DEMO_DIR}/docker-compose.yml"
  --project-directory "${ROOT_DIR}"
)
CONTROL_URL="${FORGE_CONTROL_URL:-http://127.0.0.1:4001}"
RUNTIME_URL="${FORGE_RUNTIME_URL:-http://127.0.0.1:4102}"
GATEWAY_URL="${FORGE_GATEWAY_URL:-http://127.0.0.1:4000}"
BUILD_URL="${FORGE_BUILD_URL:-http://127.0.0.1:4103}"
CONTROL_SERVICE="forge-control"
RUNTIME_SERVICE="forge-runtime"
GATEWAY_SERVICE="forge-gateway"
BUILD_SERVICE="forge-build"
STORAGE_SERVICE="forge-storage"
POSTGRES_SERVICE="postgres"
REGISTRY_SERVICE="registry"
STORAGE_URL="${FORGE_STORAGE_HOST_URL:-http://127.0.0.1:4107}"
STORAGE_BUCKET="${FORGE_STORAGE_BUCKET:-snapnote-attachments}"
STORAGE_PROJECT="${FORGE_STORAGE_PROJECT:-snapnote}"
CLI_DIR="${ROOT_DIR}/tools/forge-cli"
FORGE_BIN="${CLI_DIR}/forge"
REGISTRY="${FORGE_REGISTRY:-localhost:5000}"
API_IMAGE="${DEMO_API_IMAGE:-${REGISTRY}/snapnote/snapnote-api:v1}"
WEB_IMAGE="${DEMO_WEB_IMAGE:-${REGISTRY}/snapnote/snapnote-web:v1}"
API_HOST="api.snapnote.localhost"
APP_HOST="app.snapnote.localhost"
DB_NAME="snapnote-db"          # instance / dependency name (may contain '-')
DB_LOGICAL_NAME="snapnote_db"  # Postgres DB name ([a-z_][a-z0-9_]*)

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-demo-52.XXXXXX")"
CONFIG_HOME="${TMP_DIR}/xdg-config"
export XDG_CONFIG_HOME="${CONFIG_HOME}"
export CI="${CI:-1}"
export FORGE_PROFILE="${FORGE_PROFILE:-demo52}"
export FORGE_ENDPOINT="${FORGE_ENDPOINT:-${CONTROL_URL}}"
mkdir -p "${CONFIG_HOME}"

fail() {
  echo "Demo 52 failed: $*" >&2
  echo "--- ${GATEWAY_SERVICE} /admin/routes ---" >&2
  curl --silent --show-error "${GATEWAY_URL}/admin/routes" >&2 || true
  echo >&2
  echo "--- ${CONTROL_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=80 "${CONTROL_SERVICE}" >&2 || true
  echo "--- ${RUNTIME_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${RUNTIME_SERVICE}" >&2 || true
  echo "--- ${GATEWAY_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${GATEWAY_SERVICE}" >&2 || true
  echo "--- managed db containers ---" >&2
  docker ps --filter "label=forge.managed_db=true" --format '{{.Names}} {{.Status}}' >&2 || true
  echo "--- ${STORAGE_SERVICE} logs (tail) ---" >&2
  "${COMPOSE[@]}" logs --tail=60 "${STORAGE_SERVICE}" >&2 || true
  exit 1
}

cleanup_tmp() {
  rm -rf "${TMP_DIR}"
}
trap cleanup_tmp EXIT

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
  forge --output json "$@" >"${output}" || fail "forge $* failed"
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${output}" ||
    fail "forge $* did not emit JSON: $(cat "${output}")"
}

write_state() {
  cat >"${STATE_FILE}" <<EOF
PROJECT_SLUG=${PROJECT_SLUG}
PROJECT_ID=${PROJECT_ID}
API_DEPLOYMENT_ID=${API_DEPLOYMENT_ID}
WEB_DEPLOYMENT_ID=${WEB_DEPLOYMENT_ID}
API_IMAGE=${API_IMAGE}
WEB_IMAGE=${WEB_IMAGE}
DB_NAME=${DB_NAME}
EOF
}

read_state() {
  [[ -f "${STATE_FILE}" ]] || return 1
  # shellcheck disable=SC1090
  source "${STATE_FILE}"
}

delete_deployment() {
  local dep_id="$1"
  [[ -n "${dep_id}" ]] || return 0
  curl --silent --show-error -X DELETE \
    "${CONTROL_URL}/v1/deployments/${dep_id}" >/dev/null 2>&1 || true
  docker ps -aq --filter "label=forge.deployment_id=${dep_id}" \
    --filter "label=forge.managed=true" |
    while read -r cid; do
      [[ -n "${cid}" ]] || continue
      docker rm -f "${cid}" >/dev/null 2>&1 || true
    done
}

teardown() {
  echo "Tearing down demo 52 SnapNote..."
  if read_state; then
    delete_deployment "${API_DEPLOYMENT_ID:-}"
    delete_deployment "${WEB_DEPLOYMENT_ID:-}"
    rm -f "${STATE_FILE}"
  else
    echo "  no .demo-state; best-effort cleanup of demo=52 containers"
    docker ps -aq --filter "label=forge.managed=true" --filter "label=demo=52" |
      while read -r cid; do
        [[ -n "${cid}" ]] || continue
        docker rm -f "${cid}" >/dev/null 2>&1 || true
      done
  fi
  # Best-effort: leave managed DB containers for inspect unless explicitly removed.
  echo "Teardown complete."
}

ensure_platform() {
  echo "Ensuring Postgres, registry, Storage, Control (LocalProvisioner), Runtime, Gateway, Build..."
  "${COMPOSE[@]}" up -d "${POSTGRES_SERVICE}" "${REGISTRY_SERVICE}"
  for _ in $(seq 1 60); do
    if "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" pg_isready -U forge >/dev/null 2>&1 ||
    fail "Postgres not ready"

  local need_recreate=0
  local auth_mode pattern strategy provisioner secrets_url
  auth_mode="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_AUTH_MODE 2>/dev/null || true)"
  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  strategy="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SCHEDULER_STRATEGY 2>/dev/null || true)"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  secrets_url="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_SECRETS_URL 2>/dev/null || true)"
  if [[ "${auth_mode}" != "dev" ]]; then
    need_recreate=1
  fi
  if [[ "${pattern}" != *'{service}.snapnote.localhost'* ]]; then
    need_recreate=1
  fi
  if [[ "${strategy}" != "single-node" ]]; then
    need_recreate=1
  fi
  if [[ "${provisioner}" != "local" ]]; then
    need_recreate=1
  fi
  if [[ "${secrets_url}" != "disabled" ]]; then
    need_recreate=1
  fi
  if ! docker exec "${CONTROL_SERVICE}" test -S /var/run/docker.sock 2>/dev/null; then
    need_recreate=1
  fi

  if [[ "${need_recreate}" -eq 1 ]]; then
    echo "Recreating Control/Runtime/Gateway with demo 52 managed-DB overlay..."
    "${COMPOSE[@]}" up -d --force-recreate \
      "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  else
    echo "Control/Gateway already configured for demo 52; ensuring they are up..."
    "${COMPOSE[@]}" up -d "${CONTROL_SERVICE}" "${RUNTIME_SERVICE}" "${GATEWAY_SERVICE}"
  fi
  "${COMPOSE[@]}" up -d "${BUILD_SERVICE}" "${STORAGE_SERVICE}"

  wait_http "${CONTROL_URL}/health/ready" "Control"
  wait_http "${RUNTIME_URL}/health/ready" "Runtime"
  wait_http "${GATEWAY_URL}/health/ready" "Gateway"
  wait_http "${BUILD_URL}/health/ready" "Build" 60 || true
  wait_http "${STORAGE_URL}/health/ready" "Storage" 90

  pattern="$(docker exec "${GATEWAY_SERVICE}" printenv FORGE_HOST_PATTERN 2>/dev/null || true)"
  [[ "${pattern}" == *'{service}.snapnote.localhost'* ]] ||
    fail "gateway FORGE_HOST_PATTERN must be '{service}.snapnote.localhost' (got: ${pattern})"
  provisioner="$(docker exec "${CONTROL_SERVICE}" printenv FORGE_DB_PROVISIONER 2>/dev/null || true)"
  [[ "${provisioner}" == "local" ]] ||
    fail "control FORGE_DB_PROVISIONER must be local (got: ${provisioner})"

  ensure_storage_bucket
}

ensure_storage_bucket() {
  echo "Ensuring storage bucket ${STORAGE_BUCKET} (project=${STORAGE_PROJECT})..."
  local code
  code="$(curl --silent --show-error -o "${TMP_DIR}/bucket.json" -w '%{http_code}' \
    -H "X-Forge-Project: ${STORAGE_PROJECT}" \
    -H 'content-type: application/json' \
    -d "{\"name\":\"${STORAGE_BUCKET}\"}" \
    "${STORAGE_URL}/v1/buckets" || echo "000")"
  if [[ "${code}" != "201" && "${code}" != "200" && "${code}" != "409" ]]; then
    fail "create bucket HTTP ${code}: $(cat "${TMP_DIR}/bucket.json" 2>/dev/null || true)"
  fi
  echo "  bucket ${STORAGE_BUCKET} ready (HTTP ${code})"
}

# Prefer `forge build` when the CLI subcommand exists; otherwise docker build+push
# from source (same images forge-build would produce for this scaffold).
ensure_images() {
  if "${FORGE_BIN}" build --help >/dev/null 2>&1; then
    echo "Building via forge build --source ..."
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml api/forge.yaml --tag "${API_IMAGE}"
    ) || fail "forge build api failed"
    (
      cd "${DEMO_DIR}"
      forge build --source . --forge-yaml web.forge.yaml --tag "${WEB_IMAGE}"
    ) || fail "forge build web failed"
    return 0
  fi

  echo "forge build CLI not available; building from source with docker build+push..."
  docker build -f "${DEMO_DIR}/api/Dockerfile" -t "${API_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build api failed"
  docker push "${API_IMAGE}" || fail "docker push api failed"
  docker build -f "${DEMO_DIR}/Dockerfile.web" -t "${WEB_IMAGE}" "${DEMO_DIR}" ||
    fail "docker build web failed"
  docker push "${WEB_IMAGE}" || fail "docker push web failed"
}

ensure_cli() {
  echo "Building forge CLI..."
  make -C "${CLI_DIR}" build || fail "CLI build failed"
  [[ -x "${FORGE_BIN}" ]] || fail "forge binary missing at ${FORGE_BIN}"
  forge config set endpoint "${FORGE_ENDPOINT}" --profile "${FORGE_PROFILE}"
  forge config use "${FORGE_PROFILE}"
}

purge_stale_workloads() {
  # Leftover desired-state from prior demos leaves multiple Gateway upstreams.
  echo "Purging leftover Control deployments + managed containers..."
  "${COMPOSE[@]}" exec -T "${POSTGRES_SERVICE}" psql -U forge -d forge -v ON_ERROR_STOP=1 <<'SQL' >/dev/null \
    || fail "could not purge stale Control deployment state"
BEGIN;
DELETE FROM control.placements;
DELETE FROM control.reconcile_status;
DELETE FROM control.deployment_events;
DELETE FROM control.deployments;
COMMIT;
SQL
  docker ps -aq --filter "label=forge.managed=true" | while read -r cid; do
    [[ -n "${cid}" ]] || continue
    docker rm -f "${cid}" >/dev/null 2>&1 || true
  done
  echo "  purged Control desired-state + managed containers"
}

wait_deployment_status() {
  local dep_id="$1" want="$2" attempts="${3:-120}"
  local status="" image="" i
  for i in $(seq 1 "${attempts}"); do
    status="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${dep_id}" |
      python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')" || true
    image="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/deployments/${dep_id}" |
      python3 -c 'import json,sys; print(json.load(sys.stdin).get("image",""))')" || true
    if [[ "${status}" == "rolled_back" || "${status}" == "failed" ]]; then
      fail "deployment ${dep_id} entered terminal status=${status} image=${image}"
    fi
    if [[ "${status}" == "${want}" || ( "${want}" == "deployed" && "${status}" == "active" ) ]]; then
      echo "Deployment ${dep_id} status=${status} image=${image}"
      return 0
    fi
    sleep 1
  done
  fail "deployment ${dep_id} status=${status:-unknown} image=${image}, want ${want}"
}

refresh_routes() {
  curl --fail --silent --show-error -X POST "${GATEWAY_URL}/admin/routes/refresh" \
    >"${TMP_DIR}/refresh.json" || fail "POST /admin/routes/refresh failed"
}

wait_route_host() {
  local host="$1" attempts="${2:-90}"
  echo "Waiting for gateway route host=${host} ..."
  for _ in $(seq 1 "${attempts}"); do
    refresh_routes
    curl --fail --silent --show-error "${GATEWAY_URL}/admin/routes" \
      >"${TMP_DIR}/routes.json" || fail "GET /admin/routes failed"
    if HOST="${host}" python3 -c '
import json, os, sys
host = os.environ["HOST"].lower()
routes = json.load(open(sys.argv[1]))
sys.exit(0 if any(r.get("host", "").lower() == host for r in routes) else 1)
' "${TMP_DIR}/routes.json"; then
      echo "  route present: ${host}"
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for route host=${host}"
}

wait_host_http() {
  local host="$1" path="${2:-/}" expect="${3:-200}" attempts="${4:-60}"
  local code
  echo "Waiting for Host=${host}${path} → ${expect} ..."
  for _ in $(seq 1 "${attempts}"); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/host-body" -w '%{http_code}' \
      -H "Host: ${host}" "${GATEWAY_URL}${path}" || echo "000")"
    if [[ "${code}" == "${expect}" ]]; then
      echo "  Host ${host}${path} → ${code}"
      return 0
    fi
    sleep 1
  done
  fail "Host ${host}${path} returned HTTP ${code:-000}, want ${expect}; body=$(cat "${TMP_DIR}/host-body" 2>/dev/null || true)"
}

extract_apply_ids() {
  python3 - "${TMP_DIR}/apply.json" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
project_id = ""
for r in body.get("results", []):
    kind = r.get("kind") or ""
    name = r.get("name") or ""
    meta = (r.get("resource") or {}).get("metadata") or {}
    rid = meta.get("id") or ""
    if kind == "Project" and rid:
        project_id = rid
        print(f"PROJECT_ID={rid}")
    if kind == "Deployment" and name and rid:
        print(f"DEPLOYMENT:{name}={rid}")
if not project_id:
    # Fallback: some apply responses omit nested resource; leave empty for later lookup.
    pass
PY
}

assert_applications_ready() {
  echo "Checking applications/deployments Ready..."
  wait_deployment_status "${API_DEPLOYMENT_ID}" "deployed" 180
  wait_deployment_status "${WEB_DEPLOYMENT_ID}" "deployed" 120
  echo "  applications Ready (deployments active)"
}

provision_managed_db() {
  echo "Provisioning managed Database ${DB_NAME} (dependencies.database)..."
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID missing; cannot create managed database"
  # Instance name matches the dependency name (snapnote-db). Logical Postgres DB
  # names cannot contain '-' (platform pattern [a-z_][a-z0-9_]*).
  forge_json "${TMP_DIR}/db-create.json" --project "${PROJECT_ID}" \
    database create "${DB_NAME}" --database "${DB_LOGICAL_NAME}"
  python3 - <<'PY' "${TMP_DIR}/db-create.json" || fail "database create did not reach available"
import json, sys
body = json.load(open(sys.argv[1]))
db = body.get("database") or {}
inst = body.get("instance") or {}
status = db.get("status") or ""
inst_status = inst.get("status") or ""
assert status == "available", body
assert inst_status == "available", body
print(f"  database Ready id={db.get('id')} name={db.get('name')} instance={inst.get('id')}")
PY

  forge_json "${TMP_DIR}/db-attach.json" --project "${PROJECT_ID}" \
    database attach "${DB_NAME}" --app snapnote-api --env-var DATABASE_URL
  python3 - <<'PY' "${TMP_DIR}/db-attach.json" || fail "attach missing secretRef"
import json, sys
body = json.load(open(sys.argv[1]))
ref = body.get("secretRef") or body.get("secret_ref") or ""
assert ref, body
assert "://" not in ref, body
print(f"  attached DATABASE_URL secretRef={ref}")
PY
}

api_container_id() {
  # Runtime labels forge.deployment_id as "{service}-{shortId}-0", not the Control UUID.
  # Prefer the UUID label when present; otherwise match by image + demo label / name prefix.
  local cid
  cid="$(docker ps -q \
    --filter "label=forge.deployment_id=${API_DEPLOYMENT_ID}" \
    --filter "label=forge.managed=true" | head -n1)"
  if [[ -n "${cid}" ]]; then
    echo "${cid}"
    return 0
  fi
  local short
  short="$(python3 -c 'import sys; print(sys.argv[1].replace("-", "")[:8])' "${API_DEPLOYMENT_ID}")"
  docker ps -q --filter "label=forge.managed=true" --filter "name=forge-api-${short}-" | head -n1
}

container_env() {
  local cid="$1" key="$2"
  # Distroless images have no printenv/shell — read Config.Env via inspect.
  docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${cid}" 2>/dev/null |
    awk -F= -v k="${key}" '$1==k { print substr($0, index($0, "=")+1); exit }'
}

wait_database_url_injected() {
  local cid="" url="" i
  echo "Waiting for DATABASE_URL injection into API container..."
  for i in $(seq 1 120); do
    cid="$(api_container_id)"
    if [[ -n "${cid}" ]]; then
      url="$(container_env "${cid}" DATABASE_URL)"
      if [[ -n "${url}" ]]; then
        echo "  DATABASE_URL present on container ${cid:0:12}"
        return 0
      fi
    fi
    sleep 1
  done
  fail "DATABASE_URL never appeared on API container"
}

prove_persistence() {
  local title="persist-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  local create_body note_id code cid
  echo "Proving note persistence across API container restart..."
  create_body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"body":"persists"}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/create-note.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "${create_body}" "${GATEWAY_URL}/notes" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create note HTTP ${code}: $(cat "${TMP_DIR}/create-note.json")"
  note_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/create-note.json")"
  [[ -n "${note_id}" ]] || fail "create note missing id"

  cid="$(api_container_id)"
  [[ -n "${cid}" ]] || fail "API container missing before restart"
  echo "  restarting API container ${cid:0:12}..."
  docker restart "${cid}" >/dev/null || fail "docker restart api failed"
  # Gateway may briefly 502/503 while the container and upstream probe recover.
  wait_host_http "${API_HOST}" "/health/ready" 200 120
  refresh_routes

  code="000"
  for _ in $(seq 1 60); do
    code="$(curl --silent --show-error -o "${TMP_DIR}/list-notes.json" -w '%{http_code}' \
      -H "Host: ${API_HOST}" "${GATEWAY_URL}/notes" || echo "000")"
    if [[ "${code}" == "200" ]]; then
      break
    fi
    sleep 1
  done
  [[ "${code}" == "200" ]] || fail "list notes after restart HTTP ${code}: $(cat "${TMP_DIR}/list-notes.json" 2>/dev/null || true)"
  TITLE="${title}" NOTE_ID="${note_id}" python3 - <<'PY' "${TMP_DIR}/list-notes.json" || fail "note missing after restart"
import json, os, sys
notes = json.load(open(sys.argv[1]))
want_id = os.environ["NOTE_ID"]
want_title = os.environ["TITLE"]
match = [n for n in notes if n.get("id") == want_id]
assert match, {"want": want_id, "notes": notes}
assert match[0].get("title") == want_title, match[0]
print(f"  persisted note id={want_id} title={want_title}")
PY
}

prove_storage_roundtrip() {
  local title="attach-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  local create_body note_id att_id upload_url object_key code payload download_url
  echo "Proving attachment presign + PUT + GET against Forge Storage..."
  create_body="$(python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"body":"with file"}))' "${title}")"
  code="$(curl --silent --show-error -o "${TMP_DIR}/create-note-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d "${create_body}" "${GATEWAY_URL}/notes" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create note for attachment HTTP ${code}: $(cat "${TMP_DIR}/create-note-att.json")"
  note_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${TMP_DIR}/create-note-att.json")"

  code="$(curl --silent --show-error -o "${TMP_DIR}/create-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" -H 'content-type: application/json' \
    -d '{"filename":"lake.jpg","contentType":"image/jpeg"}' \
    "${GATEWAY_URL}/notes/${note_id}/attachments" || echo "000")"
  [[ "${code}" == "201" ]] || fail "create attachment HTTP ${code}: $(cat "${TMP_DIR}/create-att.json")"
  upload_url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["uploadUrl"])' "${TMP_DIR}/create-att.json")"
  att_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["id"])' "${TMP_DIR}/create-att.json")"
  object_key="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["attachment"]["objectKey"])' "${TMP_DIR}/create-att.json")"
  [[ -n "${upload_url}" && -n "${att_id}" && -n "${object_key}" ]] ||
    fail "attachment response incomplete: $(cat "${TMP_DIR}/create-att.json")"
  echo "  attachment id=${att_id} objectKey=${object_key}"

  payload="${TMP_DIR}/lake.jpg"
  printf 'snapnote-demo-bytes-%s' "${att_id}" >"${payload}"
  # Prefer direct storage host URL for the demo proof (public URL is SPA nginx proxy).
  # Rewrite app.snapnote.localhost:4000/storage → STORAGE_URL when present.
  local put_url="${upload_url}"
  put_url="$(UPLOAD_URL="${upload_url}" STORAGE_URL="${STORAGE_URL}" python3 - <<'PY'
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
)"
  code="$(curl --silent --show-error -o "${TMP_DIR}/put-object.json" -w '%{http_code}' \
    -X PUT -H 'content-type: image/jpeg' --data-binary @"${payload}" \
    "${put_url}" || echo "000")"
  [[ "${code}" == "201" || "${code}" == "200" ]] ||
    fail "storage PUT HTTP ${code}: $(cat "${TMP_DIR}/put-object.json" 2>/dev/null || true)"

  code="$(curl --silent --show-error -o "${TMP_DIR}/download-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}/download" || echo "000")"
  [[ "${code}" == "200" ]] || fail "download presign HTTP ${code}: $(cat "${TMP_DIR}/download-att.json")"
  download_url="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["downloadUrl"])' "${TMP_DIR}/download-att.json")"
  local get_url="${download_url}"
  get_url="$(DOWNLOAD_URL="${download_url}" STORAGE_URL="${STORAGE_URL}" python3 - <<'PY'
import os, urllib.parse
u = os.environ["DOWNLOAD_URL"]
storage = os.environ["STORAGE_URL"].rstrip("/")
parsed = urllib.parse.urlparse(u)
if parsed.path.startswith("/storage/"):
    path = parsed.path[len("/storage"):]
    print(storage + path + (("?" + parsed.query) if parsed.query else ""))
else:
    print(u)
PY
)"
  code="$(curl --silent --show-error -o "${TMP_DIR}/get-object.bin" -w '%{http_code}' \
    "${get_url}" || echo "000")"
  [[ "${code}" == "200" ]] || fail "storage GET HTTP ${code}"
  cmp -s "${payload}" "${TMP_DIR}/get-object.bin" || fail "downloaded object bytes differ from upload"

  # Streamed GET via API (project-credential path).
  code="$(curl --silent --show-error -o "${TMP_DIR}/stream-object.bin" -w '%{http_code}' \
    -H "Host: ${API_HOST}" \
    "${GATEWAY_URL}/notes/${note_id}/attachments/${att_id}/content" || echo "000")"
  [[ "${code}" == "200" ]] || fail "streamed content HTTP ${code}"
  cmp -s "${payload}" "${TMP_DIR}/stream-object.bin" || fail "streamed object bytes differ from upload"

  # Metadata list includes pending row with correct key.
  code="$(curl --silent --show-error -o "${TMP_DIR}/list-att.json" -w '%{http_code}' \
    -H "Host: ${API_HOST}" \
    "${GATEWAY_URL}/notes/${note_id}/attachments" || echo "000")"
  [[ "${code}" == "200" ]] || fail "list attachments HTTP ${code}"
  ATT_ID="${att_id}" OBJECT_KEY="${object_key}" python3 - <<'PY' "${TMP_DIR}/list-att.json" || fail "attachment metadata mismatch"
import json, os, sys
items = json.load(open(sys.argv[1]))
want_id = os.environ["ATT_ID"]
want_key = os.environ["OBJECT_KEY"]
match = [a for a in items if a.get("id") == want_id]
assert match, items
assert match[0].get("objectKey") == want_key, match[0]
assert match[0].get("status") == "pending", match[0]
print(f"  storage round-trip ok id={want_id} key={want_key} status=pending")
PY
}

deploy() {
  if [[ -f "${STATE_FILE}" ]]; then
    teardown
  fi

  ensure_platform
  ensure_cli
  ensure_images
  purge_stale_workloads

  SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
  PROJECT_NAME="SnapNote ${SUFFIX}"
  PROJECT_SLUG="snapnote-${SUFFIX}"

  echo "Rendering forge.yaml → apply (project=${PROJECT_SLUG})..."
  PROJECT_NAME="${PROJECT_NAME}" PROJECT_SLUG="${PROJECT_SLUG}" \
    API_IMAGE="${API_IMAGE}" WEB_IMAGE="${WEB_IMAGE}" \
    envsubst '${PROJECT_NAME} ${PROJECT_SLUG} ${API_IMAGE} ${WEB_IMAGE}' \
    <"${DEMO_DIR}/forge.yaml" >"${TMP_DIR}/forge.yaml"

  forge_json "${TMP_DIR}/apply.json" apply -f "${TMP_DIR}/forge.yaml"

  PROJECT_ID=""
  API_DEPLOYMENT_ID=""
  WEB_DEPLOYMENT_ID=""
  while IFS= read -r line; do
    case "${line}" in
      PROJECT_ID=*) PROJECT_ID="${line#PROJECT_ID=}" ;;
      DEPLOYMENT:snapnote-api=*) API_DEPLOYMENT_ID="${line#DEPLOYMENT:snapnote-api=}" ;;
      DEPLOYMENT:snapnote-web=*) WEB_DEPLOYMENT_ID="${line#DEPLOYMENT:snapnote-web=}" ;;
    esac
  done < <(extract_apply_ids)

  [[ -n "${API_DEPLOYMENT_ID}" ]] || fail "snapnote-api Deployment id missing from apply"
  [[ -n "${WEB_DEPLOYMENT_ID}" ]] || fail "snapnote-web Deployment id missing from apply"

  if [[ -z "${PROJECT_ID}" ]]; then
    # Resolve project UUID by slug via Control list API (auth=dev).
    PROJECT_ID="$(curl --fail --silent --show-error "${CONTROL_URL}/v1/projects" |
      PROJECT_SLUG="${PROJECT_SLUG}" python3 -c '
import json,os,sys
slug=os.environ["PROJECT_SLUG"]
for p in json.load(sys.stdin):
    if p.get("slug")==slug or p.get("name")==slug:
        print(p["id"]); break
')" || true
  fi
  [[ -n "${PROJECT_ID}" ]] || fail "PROJECT_ID missing from apply/list"
  echo "Deployments api=${API_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID} project=${PROJECT_ID}"

  provision_managed_db
  wait_database_url_injected
  assert_applications_ready
  wait_route_host "${API_HOST}" 90
  wait_route_host "${APP_HOST}" 90
  wait_host_http "${API_HOST}" "/health/ready" 200 90
  wait_host_http "${APP_HOST}" "/" 200 60

  # Optional: forge wait Ready when CLI supports it.
  if "${FORGE_BIN}" wait --help >/dev/null 2>&1; then
    forge wait "application/snapnote-api" --for=condition=Ready --timeout=2m ||
      fail "forge wait snapnote-api failed"
    forge wait "application/snapnote-web" --for=condition=Ready --timeout=2m ||
      fail "forge wait snapnote-web failed"
  fi

  write_state
  bash "${DEMO_DIR}/seed.sh" || fail "seed.sh failed"
  prove_persistence
  prove_storage_roundtrip

  echo
  echo "demo 52 deploy READY (managed Postgres + object storage attachments)"
  echo "  App:          http://${APP_HOST}:4000/"
  echo "  API:          http://${API_HOST}:4000/health/ready"
  echo "  API image:    ${API_IMAGE}"
  echo "  Web image:    ${WEB_IMAGE}"
  echo "  Database:     ${DB_NAME} (Ready)"
  echo "  Storage:      ${STORAGE_BUCKET} @ ${STORAGE_URL}"
  echo "  Deployments:  api=${API_DEPLOYMENT_ID} web=${WEB_DEPLOYMENT_ID}"
  echo "  Project:      ${PROJECT_SLUG} (${PROJECT_ID})"
}

case "${1:-}" in
  --down|down|teardown)
    teardown
    ;;
  ""|up|deploy)
    deploy
    ;;
  *)
    echo "Usage: $0 [--down]" >&2
    exit 2
    ;;
esac
