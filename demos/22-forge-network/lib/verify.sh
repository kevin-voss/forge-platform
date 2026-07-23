#!/usr/bin/env bash
# Network verification helpers for demo 22 (peer status, routes, DNS, deny counter).
# Sourced by run.sh — expects NETWORK_URL, DISCOVERY_URL, CONTROL_URL, TMP_DIR, fail().

verify_peers_converged() {
  local network="${1:-cluster-overlay}"
  local expect_peers="${2:-2}"
  local node
  for node in node-a node-b node-c; do
    curl --fail --silent --show-error \
      "${NETWORK_URL}/v1/networks/${network}/nodes/${node}/peers" \
      >"${TMP_DIR}/peers-${node}.json" || fail "GET peers for ${node} failed"
    if ! EXPECT="${expect_peers}" NODE="${node}" python3 - "${TMP_DIR}/peers-${node}.json" <<'PY'
import json, os, sys
body = json.load(open(sys.argv[1]))
peers = body.get("peers") or []
want = int(os.environ["EXPECT"])
node = os.environ["NODE"]
assert len(peers) == want, f"{node}: peers={len(peers)} want={want} body={body}"
ids = {p.get("node_id") for p in peers}
assert node not in ids, f"{node} must not peer with self"
print(f"  {node}: peer_version={body.get('peer_version')} peers={sorted(ids)}")
PY
    then
      fail "peer set for ${node} incomplete"
    fi
  done
  echo "peer mesh converged (${expect_peers} peers each)"
}

verify_transport() {
  local network="${1:-cluster-overlay}"
  local from="$2" to="$3" want="$4"
  curl --fail --silent --show-error \
    "${NETWORK_URL}/v1/networks/${network}/transport?from=${from}&to=${to}" \
    >"${TMP_DIR}/transport-${from}-${to}.json" || fail "transport ${from}->${to} failed"
  if ! WANT="${want}" python3 - "${TMP_DIR}/transport-${from}-${to}.json" <<'PY'
import json, os, sys
body = json.load(open(sys.argv[1]))
want = os.environ["WANT"]
got = (body.get("transport") or "").strip()
assert got == want, body
print(f"  transport {body.get('from')}→{body.get('to')} = {got}")
PY
  then
    fail "transport mismatch ${from}->${to}"
  fi
}

verify_node_overlay() {
  local node_id="$1"
  curl --fail --silent --show-error "${CONTROL_URL}/v1/nodes" >"${TMP_DIR}/nodes-overlay.json" ||
    fail "GET /v1/nodes failed"
  if ! NODE_ID="${node_id}" python3 - "${TMP_DIR}/nodes-overlay.json" <<'PY'
import json, os, sys
nid = os.environ["NODE_ID"]
nodes = {n["id"]: n for n in json.load(open(sys.argv[1]))}
n = nodes.get(nid)
assert n, f"missing {nid}"
assert n.get("status") in ("online", "joining"), n
net = n.get("network") or {}
cidr = net.get("cidr") or n.get("network_cidr") or ""
assert cidr.startswith("10.100."), f"{nid} overlay cidr missing: {n}"
print(f"  {nid}: status={n.get('status')} cidr={cidr} wg={bool(n.get('wireguard_public_key') or n.get('wireguardPublicKey'))}")
PY
  then
    fail "node ${node_id} missing overlay assignment"
  fi
}

verify_dns_overlay() {
  local name="$1" attempts="${2:-45}"
  local ip=""
  echo "DNS A ${name} (expect overlay 10.100.x.x) ..."
  for _ in $(seq 1 "${attempts}"); do
    dig @"127.0.0.1" -p 5053 "${name}" A +short >"${TMP_DIR}/dig-${name}.txt" 2>/dev/null || true
    ip="$(grep -E '^10\.100\.[0-9]+\.[0-9]+$' "${TMP_DIR}/dig-${name}.txt" | head -n1 || true)"
    if [[ -n "${ip}" ]]; then
      echo "  ${name} → ${ip}"
      echo "${ip}" >"${TMP_DIR}/dns-${name}.ip"
      return 0
    fi
    sleep 1
  done
  fail "DNS ${name} did not return overlay A record; answers=$(cat "${TMP_DIR}/dig-${name}.txt" 2>/dev/null || true)"
}

verify_workload_lease() {
  local network="${1:-cluster-overlay}"
  local workload_id="$2"
  curl --fail --silent --show-error \
    "${NETWORK_URL}/v1/networks/${network}/workload-leases" \
    >"${TMP_DIR}/leases.json" || fail "list workload leases failed"
  if ! WL="${workload_id}" OUT="${TMP_DIR}/lease-${workload_id}.ip" python3 - "${TMP_DIR}/leases.json" <<'PY'
import json, os, sys
wl = os.environ["WL"]
body = json.load(open(sys.argv[1]))
leases = body.get("leases") if isinstance(body, dict) else body
assert isinstance(leases, list), body
match = [l for l in leases if l.get("workload_id") == wl or l.get("workloadId") == wl]
assert match, f"no lease for {wl}: {leases}"
addr = match[0].get("address") or ""
assert addr.startswith("10.100."), match[0]
print(f"  lease {wl} → {addr} on {match[0].get('node_id')}")
open(os.environ["OUT"], "w").write(addr)
PY
  then
    fail "workload lease missing for ${workload_id}"
  fi
}

verify_policy_has_action() {
  local node_id="$1" action="$2"
  curl --fail --silent --show-error \
    "${NETWORK_URL}/v1/nodes/${node_id}/network-policy-rules" \
    >"${TMP_DIR}/rules-${node_id}.json" || fail "GET policy rules for ${node_id} failed"
  if ! ACTION="${action}" python3 - "${TMP_DIR}/rules-${node_id}.json" <<'PY'
import json, os, sys
action = os.environ["ACTION"]
rs = json.load(open(sys.argv[1]))
rules = rs.get("rules") or []
assert any(r.get("action") == action for r in rules), rs
print(f"  node rules generation={rs.get('generation')} has action={action} (count={len(rules)})")
PY
  then
    fail "expected ${action} rule on ${node_id}"
  fi
}

verify_denied_counter_bumped() {
  local before="$1"
  curl --fail --silent --show-error "${NETWORK_URL}/metrics" >"${TMP_DIR}/metrics.txt" ||
    fail "GET /metrics failed"
  if ! BEFORE="${before}" python3 - "${TMP_DIR}/metrics.txt" <<'PY'
import os, re, sys
text = open(sys.argv[1]).read()
m = re.search(r'^forge_network_policy_denied_total\s+(\d+(?:\.\d+)?)', text, re.M)
assert m, text
got = float(m.group(1))
before = float(os.environ["BEFORE"])
assert got > before, f"denied_total={got} before={before}"
print(f"  forge_network_policy_denied_total {before} → {got}")
PY
  then
    fail "forge_network_policy_denied_total did not increase"
  fi
}

read_denied_counter() {
  curl --fail --silent --show-error "${NETWORK_URL}/metrics" >"${TMP_DIR}/metrics.txt" ||
    fail "GET /metrics failed"
  python3 - "${TMP_DIR}/metrics.txt" <<'PY'
import re, sys
text = open(sys.argv[1]).read()
m = re.search(r'^forge_network_policy_denied_total\s+(\d+(?:\.\d+)?)', text, re.M)
print(m.group(1) if m else "0")
PY
}

verify_peers_exclude() {
  local network="${1:-cluster-overlay}"
  local viewer="$2"
  local missing="$3"
  curl --fail --silent --show-error \
    "${NETWORK_URL}/v1/networks/${network}/nodes/${viewer}/peers" \
    >"${TMP_DIR}/peers-after-${viewer}.json" || fail "GET peers after leave failed"
  if ! MISSING="${missing}" python3 - "${TMP_DIR}/peers-after-${viewer}.json" <<'PY'
import json, os, sys
missing = os.environ["MISSING"]
peers = json.load(open(sys.argv[1])).get("peers") or []
ids = {p.get("node_id") for p in peers}
assert missing not in ids, ids
print(f"  peers={sorted(ids)} (no {missing})")
PY
  then
    fail "stale peer ${missing} still present for ${viewer}"
  fi
}

verify_endpoint_unready_or_gone() {
  local project="$1" env_name="$2" service="$3" endpoint_id="$4" attempts="${5:-60}"
  local phase=""
  echo "Waiting for endpoint ${endpoint_id} Unready/gone ..."
  for _ in $(seq 1 "${attempts}"); do
    curl --silent --show-error \
      "${DISCOVERY_URL}/v1/projects/${project}/environments/${env_name}/services/${service}/endpoints?ready=false" \
      >"${TMP_DIR}/eps-all.json" 2>/dev/null || true
    phase="$(EP_ID="${endpoint_id}" python3 - "${TMP_DIR}/eps-all.json" <<'PY'
import json, os, sys
try:
    items = json.load(open(sys.argv[1]))
except Exception:
    print("missing")
    raise SystemExit
ep = os.environ["EP_ID"]
by = {i.get("id"): i for i in items}
if ep not in by:
    print("gone")
else:
    row = by[ep]
    if row.get("phase") == "Unready" or row.get("ready") is False:
        print("unready")
    else:
        print(row.get("phase") or "ready")
PY
)"
    if [[ "${phase}" == "unready" || "${phase}" == "gone" ]]; then
      echo "  endpoint ${endpoint_id}: ${phase}"
      return 0
    fi
    sleep 1
  done
  fail "endpoint ${endpoint_id} still ready after node loss (phase=${phase:-unknown})"
}
