#!/usr/bin/env bash
# Demo 13 acceptance — 8 falsifiable assertions against a running forge-storage.
# Intended to be invoked by run.sh after readiness. Exit 0 only when all pass.
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${DEMO_DIR}/../.." && pwd)"

STORAGE_URL="${FORGE_STORAGE_URL:-http://127.0.0.1:4107}"
PROJECT_ID="${FORGE_STORAGE_PROJECT:-demo-13}"
BUCKET="${FORGE_STORAGE_BUCKET:-artifacts}"
OBJECT_KEY="${FORGE_STORAGE_OBJECT_KEY:-big.bin}"
PERSIST_KEY="${FORGE_STORAGE_PERSIST_KEY:-persist.bin}"
# 50 MiB — generated at runtime (never committed).
OBJECT_BYTES="${FORGE_STORAGE_OBJECT_BYTES:-52428800}"
COMPOSE_FILE="${DEMO_DIR}/compose.yaml"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-storage-accept.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

PASS=0
FAIL=0

pass() {
  PASS=$((PASS + 1))
  echo "  PASS: $*"
}

fail() {
  FAIL=$((FAIL + 1))
  echo "  FAIL: $*" >&2
  echo "acceptance summary: ${PASS} passed, ${FAIL} failed" >&2
  exit 1
}

step() {
  echo
  echo "[$1] $2"
}

hdr_project() {
  printf 'X-Forge-Project: %s' "${PROJECT_ID}"
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

http_code() {
  # usage: http_code METHOD URL [curl args...]
  local method="$1" url="$2"
  shift 2
  curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    --request "${method}" "${url}" "$@"
}

http_body() {
  # usage: http_body OUTFILE METHOD URL [curl args...] → prints status
  local out="$1" method="$2" url="$3"
  shift 3
  curl --silent --show-error --output "${out}" --write-out '%{http_code}' \
    --request "${method}" "${url}" "$@"
}

assert_openapi_parses() {
  step "0" "OpenAPI contract parses"
  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-storage.openapi.yaml" <<'PY'
import sys, yaml
path = sys.argv[1]
doc = yaml.safe_load(open(path))
assert doc.get("openapi"), "missing openapi version"
assert "/v1/buckets" in doc.get("paths", {}), "missing /v1/buckets"
assert any("/sign" in p for p in doc.get("paths", {})), "missing sign path"
print("openapi ok")
PY
  then
    fail "forge-storage.openapi.yaml did not parse"
  fi
  pass "OpenAPI YAML parses and documents bucket + sign paths"
}

step_create_bucket() {
  step "1" "create bucket \"${BUCKET}\""
  local status body
  body="${TMP_DIR}/bucket.json"
  status="$(http_body "${body}" POST "${STORAGE_URL}/v1/buckets" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d "{\"name\":\"${BUCKET}\"}")"
  [[ "${status}" == "201" ]] || fail "create bucket HTTP ${status}: $(cat "${body}")"
  python3 -c 'import json,sys; b=json.load(open(sys.argv[1])); assert b["name"]==sys.argv[2]' \
    "${body}" "${BUCKET}" || fail "bucket response missing name"
  pass "bucket ${BUCKET} created (HTTP 201)"
}

step_upload_large() {
  step "2" "upload ${OBJECT_BYTES} byte streamed object (${OBJECT_KEY})"
  local fixture="${TMP_DIR}/${OBJECT_KEY}"
  echo "  generating fixture (${OBJECT_BYTES} bytes) ..."
  head -c "${OBJECT_BYTES}" /dev/urandom >"${fixture}"
  local expected_sha
  expected_sha="$(sha256_file "${fixture}")"
  echo "${expected_sha}" >"${TMP_DIR}/expected.sha256"

  local start end elapsed status body
  body="${TMP_DIR}/put.json"
  start="$(python3 -c 'import time; print(time.time())')"
  # --data-binary @file streams from disk (not fully buffered by the shell).
  status="$(curl --silent --show-error --output "${body}" --write-out '%{http_code}' \
    --request PUT \
    --header "$(hdr_project)" \
    --header "Content-Type: application/octet-stream" \
    --header "X-Expected-SHA256: ${expected_sha}" \
    --data-binary @"${fixture}" \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}")"
  end="$(python3 -c 'import time; print(time.time())')"
  elapsed="$(python3 -c "print(round(${end}-${start}, 3))")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "PUT large object HTTP ${status}: $(cat "${body}")"
  python3 -c 'import json,sys; o=json.load(open(sys.argv[1])); assert o["sha256"]==sys.argv[2]' \
    "${body}" "${expected_sha}" || fail "PUT response sha256 mismatch"
  echo "  upload elapsed=${elapsed}s (timing note only)"
  pass "streamed upload of ${OBJECT_BYTES} bytes (HTTP ${status}, sha=${expected_sha:0:12}…)"
}

step_download_compare() {
  step "3" "download streamed object and byte-compare"
  local fixture="${TMP_DIR}/${OBJECT_KEY}"
  local download="${TMP_DIR}/download.bin"
  local status
  status="$(curl --silent --show-error --output "${download}" --write-out '%{http_code}' \
    --request GET \
    --header "$(hdr_project)" \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}")"
  [[ "${status}" == "200" ]] || fail "GET object HTTP ${status}"
  cmp -s "${fixture}" "${download}" || fail "downloaded bytes differ from upload fixture"
  local got_size
  got_size="$(wc -c <"${download}" | tr -d ' ')"
  [[ "${got_size}" == "${OBJECT_BYTES}" ]] ||
    fail "download size ${got_size} != ${OBJECT_BYTES}"
  pass "streamed download byte-identical (${got_size} bytes)"
}

step_checksum_etag() {
  step "4" "verify SHA-256 via ETag matches client hash"
  local expected
  expected="$(cat "${TMP_DIR}/expected.sha256")"
  local headers etag xsha
  headers="${TMP_DIR}/head.headers"
  curl --silent --show-error --head \
    --header "$(hdr_project)" \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}" >"${headers}"
  # BSD awk (macOS) has no IGNORECASE — match lowercase HTTP/1.1 header names.
  etag="$(awk 'tolower($0) ~ /^etag:/{sub(/\r$/,""); sub(/^[^:]+:[[:space:]]*/,""); print; exit}' "${headers}" | tr -d '"')"
  xsha="$(awk 'tolower($0) ~ /^x-content-sha256:/{sub(/\r$/,""); sub(/^[^:]+:[[:space:]]*/,""); print; exit}' "${headers}")"
  [[ -n "${etag}" ]] || fail "missing ETag header"
  [[ "${etag}" == "${expected}" ]] || fail "ETag ${etag} != client sha ${expected}"
  [[ "${xsha}" == "${expected}" ]] || fail "X-Content-SHA256 ${xsha} != client sha ${expected}"
  pass "ETag / X-Content-SHA256 match client SHA-256"
}

step_byte_range() {
  step "5" "GET Range bytes=0-1023 → 206, exactly 1024 bytes"
  local range_out="${TMP_DIR}/range.bin"
  local headers="${TMP_DIR}/range.headers"
  local status
  status="$(curl --silent --show-error --output "${range_out}" --write-out '%{http_code}' \
    --dump-header "${headers}" \
    --request GET \
    --header "$(hdr_project)" \
    --header 'Range: bytes=0-1023' \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}")"
  [[ "${status}" == "206" ]] || fail "Range GET HTTP ${status}, want 206"
  local got
  got="$(wc -c <"${range_out}" | tr -d ' ')"
  [[ "${got}" == "1024" ]] || fail "range body length ${got}, want 1024"
  # First 1024 bytes of fixture must match.
  head -c 1024 "${TMP_DIR}/${OBJECT_KEY}" >"${TMP_DIR}/expect-range.bin"
  cmp -s "${TMP_DIR}/expect-range.bin" "${range_out}" || fail "range bytes differ from fixture prefix"
  awk 'tolower($0) ~ /^content-range:[[:space:]]*bytes 0-1023\//{found=1} END{exit !found}' "${headers}" ||
    fail "missing/incorrect Content-Range header"
  pass "Range bytes=0-1023 → 206 Partial Content (1024 bytes)"
}

step_expired_token() {
  step "6" "issue GET token ttl=1s, sleep, use → 401 token_expired"
  local sign_body="${TMP_DIR}/sign.json"
  local status
  status="$(http_body "${sign_body}" POST \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}/sign" \
    -H "$(hdr_project)" \
    -H 'content-type: application/json' \
    -d '{"method":"GET","ttl_seconds":1}')"
  [[ "${status}" == "200" ]] || fail "sign HTTP ${status}: $(cat "${sign_body}")"
  local token
  token="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "${sign_body}")"
  [[ -n "${token}" ]] || fail "sign response missing token"
  echo "  sleeping 2s for token expiry (clock skew=0) ..."
  sleep 2
  local err="${TMP_DIR}/expired.json"
  status="$(http_body "${err}" GET \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}?token=${token}")"
  [[ "${status}" == "401" ]] || fail "expired token HTTP ${status}, want 401: $(cat "${err}")"
  python3 -c 'import json,sys; c=json.load(open(sys.argv[1])).get("code"); assert c=="token_expired", c' \
    "${err}" || fail "expected code=token_expired: $(cat "${err}")"
  pass "expired signed token rejected (401 token_expired)"
}

step_delete_object() {
  step "7" "delete object → 204; GET → 404"
  local status
  status="$(http_code DELETE \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}" \
    -H "$(hdr_project)")"
  [[ "${status}" == "204" ]] || fail "DELETE object HTTP ${status}, want 204"
  status="$(http_code GET \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${OBJECT_KEY}" \
    -H "$(hdr_project)")"
  [[ "${status}" == "404" ]] || fail "GET after delete HTTP ${status}, want 404"
  pass "object deleted (204) and subsequent GET is 404"
}

step_restart_durability() {
  step "8" "re-upload + compose restart → object + bucket survive"
  # After delete, upload a small durable object, restart, assert survival.
  local persist="${TMP_DIR}/${PERSIST_KEY}"
  printf 'demo-13-durability-%s\n' "$(date -u +%Y%m%dT%H%M%SZ)" >"${persist}"
  local status body
  body="${TMP_DIR}/persist-put.json"
  status="$(curl --silent --show-error --output "${body}" --write-out '%{http_code}' \
    --request PUT \
    --header "$(hdr_project)" \
    --header "Content-Type: application/octet-stream" \
    --data-binary @"${persist}" \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${PERSIST_KEY}")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "persist PUT HTTP ${status}: $(cat "${body}")"

  echo "  docker compose restart forge-storage ..."
  docker compose -f "${COMPOSE_FILE}" --project-directory "${DEMO_DIR}" \
    restart forge-storage >/dev/null

  local ready=0
  for _ in $(seq 1 60); do
    if curl --fail --silent --show-error "${STORAGE_URL}/health/ready" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "storage not ready after restart"

  # Bucket still listed.
  body="${TMP_DIR}/buckets-after.json"
  status="$(http_body "${body}" GET "${STORAGE_URL}/v1/buckets" -H "$(hdr_project)")"
  [[ "${status}" == "200" ]] || fail "list buckets after restart HTTP ${status}"
  python3 -c 'import json,sys; names=[b["name"] for b in json.load(open(sys.argv[1]))["buckets"]]; assert sys.argv[2] in names, names' \
    "${body}" "${BUCKET}" || fail "bucket ${BUCKET} missing after restart"

  # Persist object still downloadable and byte-identical.
  local download="${TMP_DIR}/persist-download.bin"
  status="$(curl --silent --show-error --output "${download}" --write-out '%{http_code}' \
    --request GET \
    --header "$(hdr_project)" \
    "${STORAGE_URL}/v1/buckets/${BUCKET}/objects/${PERSIST_KEY}")"
  [[ "${status}" == "200" ]] || fail "GET persist object after restart HTTP ${status}"
  cmp -s "${persist}" "${download}" || fail "persist object bytes changed across restart"
  pass "restart durability: bucket + re-uploaded object survive"
}

main() {
  echo "== Demo 13 acceptance (forge-storage @ ${STORAGE_URL}, project=${PROJECT_ID}) =="
  assert_openapi_parses
  step_create_bucket
  step_upload_large
  step_download_compare
  step_checksum_etag
  step_byte_range
  step_expired_token
  step_delete_object
  step_restart_durability
  echo
  echo "acceptance summary: ${PASS} passed, ${FAIL} failed"
  [[ "${FAIL}" -eq 0 ]] || exit 1
  echo "demo 13 acceptance PASSED (8/8 steps)"
}

main "$@"
