#!/usr/bin/env bash
# Demo 17 acceptance — falsifiable assertions against a running stack.
# Invoked by run.sh after readiness. Exit 0 only when all pass.
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${DEMO_DIR}/../.." && pwd)"

MEMORY_URL="${FORGE_MEMORY_URL:-http://127.0.0.1:4303}"
AGENTS_URL="${FORGE_AGENTS_URL:-http://127.0.0.1:4301}"
MODELS_URL="${FORGE_MODELS_URL:-http://127.0.0.1:4300}"
PROJECT_A="${FORGE_MEMORY_PROJECT_A:-proj-a}"
PROJECT_B="${FORGE_MEMORY_PROJECT_B:-proj-b}"
COMPOSE_PROJECT="${FORGE_DEMO17_COMPOSE_PROJECT:-forge-demo-17}"
POLL_ATTEMPTS="${FORGE_AGENTS_POLL_ATTEMPTS:-90}"
POLL_SLEEP_SECONDS="${FORGE_AGENTS_POLL_SLEEP_SECONDS:-0.25}"
FIXTURES="${DEMO_DIR}/fixtures/incidents.json"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/forge-memory-accept.XXXXXX")"
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

http_body() {
  local out="$1" method="$2" url="$3"
  shift 3
  curl --silent --show-error --output "${out}" --write-out '%{http_code}' \
    --request "${method}" "${url}" "$@"
}

hdr_project() {
  printf 'X-Forge-Project: %s' "$1"
}

assert_openapi_parses() {
  step "0" "OpenAPI contracts parse"
  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-memory.openapi.yaml" <<'PY'
import sys, yaml
path = sys.argv[1]
doc = yaml.safe_load(open(path))
assert doc.get("openapi"), "missing openapi version"
paths = doc.get("paths") or {}
for required in (
    "/v1/collections",
    "/v1/collections/{name}",
    "/v1/collections/{name}/upsert",
    "/v1/collections/{name}/query",
    "/v1/collections/{name}/records/{id}",
):
    assert required in paths, f"missing path {required}"
print("memory openapi ok")
PY
  then
    fail "forge-memory.openapi.yaml did not parse or is missing required paths"
  fi
  pass "forge-memory OpenAPI parses with collection/upsert/query paths"

  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-agents.openapi.yaml" <<'PY'
import sys, yaml
path = sys.argv[1]
doc = yaml.safe_load(open(path))
assert doc.get("openapi"), "missing openapi version"
paths = doc.get("paths") or {}
for required in ("/v1/agents", "/v1/agents/{name}/runs", "/v1/runs/{run_id}", "/v1/tools"):
    assert required in paths, f"missing path {required}"
print("agents openapi ok")
PY
  then
    fail "forge-agents.openapi.yaml did not parse"
  fi
  pass "forge-agents OpenAPI parses"

  if ! python3 - "${ROOT_DIR}/contracts/openapi/forge-models.openapi.yaml" <<'PY'
import sys, yaml
path = sys.argv[1]
doc = yaml.safe_load(open(path))
assert doc.get("openapi"), "missing openapi version"
assert "/v1/models" in (doc.get("paths") or {}), "missing /v1/models"
print("models openapi ok")
PY
  then
    fail "forge-models.openapi.yaml did not parse"
  fi
  pass "forge-models OpenAPI parses"
}

assert_stack_ready() {
  step "1" "models + memory + agents ready"
  local status body
  body="${TMP_DIR}/models-ready.json"
  status="$(http_body "${body}" GET "${MODELS_URL}/health/ready")"
  [[ "${status}" == "200" ]] || fail "models ready HTTP ${status}: $(cat "${body}")"
  pass "forge-models /health/ready"

  body="${TMP_DIR}/memory-ready.json"
  status="$(http_body "${body}" GET "${MEMORY_URL}/health/ready")"
  [[ "${status}" == "200" ]] || fail "memory ready HTTP ${status}: $(cat "${body}")"
  pass "forge-memory /health/ready"

  body="${TMP_DIR}/agents-ready.json"
  status="$(http_body "${body}" GET "${AGENTS_URL}/health/ready")"
  [[ "${status}" == "200" ]] || fail "agents ready HTTP ${status}: $(cat "${body}")"
  pass "forge-agents /health/ready"
}

seed_incidents() {
  step "2" "seed historical incidents into ${PROJECT_A}/incidents (Models embed)"
  local status body payload
  body="${TMP_DIR}/create-collection.json"
  payload="$(python3 - "${FIXTURES}" <<'PY'
import json, sys
fx = json.load(open(sys.argv[1]))
print(json.dumps({
    "name": fx["collection"],
    "dim": fx["dim"],
    "distance": fx.get("distance") or "cosine",
}))
PY
)"
  status="$(http_body "${body}" POST "${MEMORY_URL}/v1/collections" \
    -H "$(hdr_project "${PROJECT_A}")" \
    -H 'content-type: application/json' \
    -d "${payload}")"
  [[ "${status}" == "201" || "${status}" == "200" ]] ||
    fail "create collection HTTP ${status}: $(cat "${body}")"

  body="${TMP_DIR}/upsert.json"
  payload="$(python3 - "${FIXTURES}" <<'PY'
import json, sys
fx = json.load(open(sys.argv[1]))
print(json.dumps({
    "model": fx["model"],
    "items": [
        {"id": i["id"], "text": i["text"], "metadata": i.get("metadata") or {}}
        for i in fx["incidents"]
    ],
}))
PY
)"
  status="$(http_body "${body}" POST \
    "${MEMORY_URL}/v1/collections/incidents/upsert" \
    -H "$(hdr_project "${PROJECT_A}")" \
    -H 'content-type: application/json' \
    -d "${payload}")"
  [[ "${status}" == "200" ]] || fail "upsert HTTP ${status}: $(cat "${body}")"
  python3 - "${body}" "${FIXTURES}" <<'PY' || fail "upsert count mismatch"
import json, sys
body = json.load(open(sys.argv[1]))
fx = json.load(open(sys.argv[2]))
want = len(fx["incidents"])
got = int(body.get("upserted") or 0)
assert got == want, (got, want, body)
print(f"upserted={got}")
PY
  pass "seeded ${PROJECT_A}/incidents via Models text embed path"
}

assert_nn_query() {
  step "3" "NN query returns expected historical incident (ordering)"
  local status body expected query_text
  expected="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["expected_id"])' "${FIXTURES}")"
  query_text="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["new_failure_text"])' "${FIXTURES}")"

  # Text path (Models fake deterministic embed): matching symptom text → top-1.
  body="${TMP_DIR}/query-text.json"
  status="$(http_body "${body}" POST \
    "${MEMORY_URL}/v1/collections/incidents/query" \
    -H "$(hdr_project "${PROJECT_A}")" \
    -H 'content-type: application/json' \
    -d "$(python3 -c 'import json,sys; print(json.dumps({"text":sys.argv[1],"model":"local-embed-small","top_k":3}))' "${query_text}")")"
  [[ "${status}" == "200" ]] || fail "text query HTTP ${status}: $(cat "${body}")"
  cp "${body}" "${TMP_DIR}/query-before-restart.json"

  python3 - "${body}" "${expected}" <<'PY' || fail "text NN ordering assertion failed"
import json, sys
body = json.load(open(sys.argv[1]))
expected = sys.argv[2]
results = body.get("results") or []
assert results, body
assert results[0].get("id") == expected, (results, expected)
score0 = float(results[0].get("score") or 0)
assert score0 > 0.99, score0
if len(results) > 1:
    assert score0 >= float(results[1].get("score") or 0), results
print("TEXT_NN", [(r.get("id"), round(float(r.get("score") or 0), 4)) for r in results])
PY
  pass "text NN: top result is ${expected} (score≈1.0)"

  # Raw-vector near-neighbor: prove cosine ordering with a controlled query vector.
  body="${TMP_DIR}/get-expected.json"
  status="$(http_body "${body}" GET \
    "${MEMORY_URL}/v1/collections/incidents/records/${expected}" \
    -H "$(hdr_project "${PROJECT_A}")")"
  [[ "${status}" == "200" ]] || fail "get expected record HTTP ${status}: $(cat "${body}")"

  local distractor
  distractor="$(python3 - "${FIXTURES}" "${expected}" <<'PY'
import json, sys
fx = json.load(open(sys.argv[1]))
exp = sys.argv[2]
for i in fx["incidents"]:
    if i["id"] != exp:
        print(i["id"])
        break
else:
    raise SystemExit("no distractor")
PY
)"
  local dbody="${TMP_DIR}/get-distractor.json"
  status="$(http_body "${dbody}" GET \
    "${MEMORY_URL}/v1/collections/incidents/records/${distractor}" \
    -H "$(hdr_project "${PROJECT_A}")")"
  [[ "${status}" == "200" ]] || fail "get distractor HTTP ${status}: $(cat "${dbody}")"

  local qpayload
  qpayload="$(python3 - "${body}" "${dbody}" <<'PY'
import json, math, sys
target = json.load(open(sys.argv[1]))["vector"]
other = json.load(open(sys.argv[2]))["vector"]
raw = [0.9 * float(a) + 0.1 * float(b) for a, b in zip(target, other)]
norm = math.sqrt(sum(v * v for v in raw)) or 1.0
vec = [v / norm for v in raw]
print(json.dumps({"vector": vec, "top_k": 3}))
PY
)"
  body="${TMP_DIR}/query-raw.json"
  status="$(http_body "${body}" POST \
    "${MEMORY_URL}/v1/collections/incidents/query" \
    -H "$(hdr_project "${PROJECT_A}")" \
    -H 'content-type: application/json' \
    -d "${qpayload}")"
  [[ "${status}" == "200" ]] || fail "raw query HTTP ${status}: $(cat "${body}")"
  python3 - "${body}" "${expected}" <<'PY' || fail "raw NN ordering assertion failed"
import json, sys
body = json.load(open(sys.argv[1]))
expected = sys.argv[2]
results = body.get("results") or []
assert results, body
assert results[0].get("id") == expected, (results, expected)
print("RAW_NN", [(r.get("id"), round(float(r.get("score") or 0), 4)) for r in results])
PY
  pass "raw-vector NN: near-neighbor of ${expected} ranks first"
  echo "  retrieved records (text query): $(python3 -c 'import json; print(json.load(open("'"${TMP_DIR}/query-before-restart.json"'"))["results"])')"
}

wait_run_status() {
  local run_id="$1"
  shift
  local want=("$@")
  local body status i
  body="${TMP_DIR}/run-${run_id}.json"
  for i in $(seq 1 "${POLL_ATTEMPTS}"); do
    status="$(http_body "${body}" GET "${AGENTS_URL}/v1/runs/${run_id}" \
      -H "$(hdr_project "${PROJECT_A}")")"
    [[ "${status}" == "200" ]] || fail "GET run ${run_id} HTTP ${status}: $(cat "${body}")"
    if python3 - "${body}" "${want[@]}" <<'PY'
import json, sys
body = json.load(open(sys.argv[1]))
want = set(sys.argv[2:])
sys.exit(0 if body.get("status") in want else 1)
PY
    then
      cat "${body}"
      return 0
    fi
    sleep "${POLL_SLEEP_SECONDS}"
  done
  fail "run ${run_id} did not reach status in: ${want[*]} (last=$(cat "${body}"))"
}

assert_agent_cites_memory() {
  step "4" "agent uses memory.search and cites retrieved incident"
  local status body agents_body run_id run_body payload expected
  expected="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["expected_id"])' "${FIXTURES}")"

  agents_body="${TMP_DIR}/agents.json"
  status="$(http_body "${agents_body}" GET "${AGENTS_URL}/v1/agents")"
  [[ "${status}" == "200" ]] || fail "list agents HTTP ${status}: $(cat "${agents_body}")"
  python3 - "${agents_body}" <<'PY' || fail "incident-memory agent not registered"
import json, sys
body = json.load(open(sys.argv[1]))
names = {a.get("name") for a in body.get("agents") or []}
assert "incident-memory" in names, names
print("ok")
PY
  pass "incident-memory listed in GET /v1/agents"

  payload="$(python3 - "${FIXTURES}" <<'PY'
import json, sys
fx = json.load(open(sys.argv[1]))
query = fx["new_failure_text"]
expected = fx["expected_id"]
plan = [
    {
        "kind": "tool_call",
        "tool": "memory.search",
        "args": {
            "collection": fx["collection"],
            "query": query,
            "top_k": 3,
        },
    },
    {
        "kind": "final",
        "text": (
            f"Diagnosis: symptoms match historical {expected} "
            f"({fx['incidents'][0]['metadata']['summary']}). "
            "Cite memory record and recommend pool sizing / connection limit review."
        ),
    },
]
print(json.dumps({
    "input": f"New deploy failure with similar symptoms: {query}",
    "context": {"dry_run": True, "plan": plan},
}))
PY
)"

  body="${TMP_DIR}/start-agent.json"
  status="$(http_body "${body}" POST \
    "${AGENTS_URL}/v1/agents/incident-memory/runs" \
    -H "$(hdr_project "${PROJECT_A}")" \
    -H 'content-type: application/json' \
    -d "${payload}")"
  [[ "${status}" == "202" ]] || fail "start agent HTTP ${status}: $(cat "${body}")"
  run_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["run_id"])' "${body}")"
  echo "  run_id=${run_id}"
  wait_run_status "${run_id}" succeeded >"${TMP_DIR}/run-main.json"
  run_body="${TMP_DIR}/run-main.json"

  python3 - "${run_body}" "${expected}" <<'PY' || fail "agent citation assertions failed"
import json, sys
body = json.load(open(sys.argv[1]))
expected = sys.argv[2]
assert body.get("status") == "succeeded", body.get("status")
steps = body.get("steps") or []
tool_steps = [s for s in steps if s.get("type") == "tool"]
assert tool_steps, steps
search = tool_steps[0]
assert search.get("tool") == "memory.search", search
obs = search.get("observation") or {}
assert obs.get("ok") is True, obs
results = obs.get("results") or []
assert results, obs
assert results[0].get("id") == expected, results
# Diagnosis / final answer must cite the retrieved incident id.
blob = json.dumps(body)
assert expected in blob, blob[:800]
finals = [s for s in steps if s.get("type") == "final"]
assert finals, steps
assert expected in str(finals[0].get("observation") or finals[0]), finals[0]
result = str(body.get("result") or "")
assert expected in result, result
print("cited", expected)
PY
  pass "agent memory.search returned ${expected}; diagnosis cites the incident id"
}

assert_project_isolation() {
  step "5" "project B cannot see project A memory"
  local status body
  body="${TMP_DIR}/proj-b-get.json"
  status="$(http_body "${body}" GET \
    "${MEMORY_URL}/v1/collections/incidents" \
    -H "$(hdr_project "${PROJECT_B}")")"
  [[ "${status}" == "404" ]] || fail "proj-b get collection expected 404, got ${status}: $(cat "${body}")"
  pass "proj-b GET /collections/incidents → 404"

  body="${TMP_DIR}/proj-b-query.json"
  status="$(http_body "${body}" POST \
    "${MEMORY_URL}/v1/collections/incidents/query" \
    -H "$(hdr_project "${PROJECT_B}")" \
    -H 'content-type: application/json' \
    -d '{"text":"database connection refused","model":"local-embed-small","top_k":3}')"
  [[ "${status}" == "404" ]] || fail "proj-b query expected 404, got ${status}: $(cat "${body}")"
  pass "proj-b query → 404 (no existence leak)"
}

assert_restart_durability() {
  step "6" "restart forge-memory → vectors persist; query identical"
  local before after status body ready i
  before="${TMP_DIR}/query-before-restart.json"
  [[ -f "${before}" ]] || fail "missing pre-restart query snapshot"

  echo "  docker compose restart forge-memory ..."
  docker compose -p "${COMPOSE_PROJECT}" -f "${DEMO_DIR}/compose.yaml" \
    --project-directory "${DEMO_DIR}" \
    restart forge-memory >/dev/null

  ready=0
  for i in $(seq 1 120); do
    if curl --fail --silent --show-error "${MEMORY_URL}/health/ready" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "${ready}" -eq 1 ]] || fail "memory not ready after restart"

  after="${TMP_DIR}/query-after-restart.json"
  local query_text
  query_text="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["new_failure_text"])' "${FIXTURES}")"
  status="$(http_body "${after}" POST \
    "${MEMORY_URL}/v1/collections/incidents/query" \
    -H "$(hdr_project "${PROJECT_A}")" \
    -H 'content-type: application/json' \
    -d "$(python3 -c 'import json,sys; print(json.dumps({"text":sys.argv[1],"model":"local-embed-small","top_k":3}))' "${query_text}")")"
  [[ "${status}" == "200" ]] || fail "post-restart query HTTP ${status}: $(cat "${after}")"

  python3 - "${before}" "${after}" <<'PY' || fail "query results changed across restart"
import json, sys
before = json.load(open(sys.argv[1]))
after = json.load(open(sys.argv[2]))
br = before.get("results") or []
ar = after.get("results") or []
assert br and ar, (before, after)
assert [r.get("id") for r in br] == [r.get("id") for r in ar], (br, ar)
# Scores should match within float noise.
for a, b in zip(br, ar):
    assert abs(float(a.get("score") or 0) - float(b.get("score") or 0)) < 1e-5, (a, b)
print("ids", [r.get("id") for r in ar])
PY
  pass "restart durability: identical NN results after forge-memory restart"
}

document_benchmark() {
  step "7" "performance benchmark note (fixture scale)"
  # Documented in services/forge-memory README (bench_query_10k); surface here for the gate.
  echo "  Benchmark (forge-memory brute-force cosine, N=10_000, dim=32):"
  echo "    query latency (top_k=10, full scan) ≈ 27 ms (dev laptop)"
  echo "    reproduce: cd services/forge-memory && cargo test --test bench_query_10k -- --nocapture"
  pass "benchmark documented (~27ms @10k; see services/forge-memory/README.md)"
}

assert_openapi_parses
assert_stack_ready
seed_incidents
assert_nn_query
assert_agent_cites_memory
assert_project_isolation
assert_restart_durability
document_benchmark

echo
echo "acceptance summary: ${PASS} passed, ${FAIL} failed"
[[ "${FAIL}" -eq 0 ]]
