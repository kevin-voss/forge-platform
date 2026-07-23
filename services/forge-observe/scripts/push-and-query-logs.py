#!/usr/bin/env python3
"""Push correlated log lines to Loki and verify Observe GET /v1/logs filters."""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def http_json(method: str, url: str, body: dict | None = None, timeout: float = 10.0):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read()
        if not raw:
            return resp.status, None
        return resp.status, json.loads(raw.decode("utf-8"))


def push_logs(loki: str, project: str, deployment: str, trace_id: str, request_id: str) -> None:
    now_ns = time.time_ns()
    streams = []
    for i, service in enumerate(("control", "gateway", "runtime")):
        ts = str(now_ns + i * 1_000_000)
        line = {
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "level": "info",
            "service": service,
            "message": f"correlated line from {service}",
            "trace_id": trace_id,
            "request_id": request_id,
            "forge.project": project,
            "forge.deployment": deployment,
            "forge.service": service,
        }
        streams.append(
            {
                "stream": {
                    "job": "forge-observe-itest",
                    "forge_project": project,
                    "forge_deployment": deployment,
                    "forge_service": service,
                },
                "values": [[ts, json.dumps(line)]],
            }
        )
        # Second deployment for negative filter check (same project, other dpl).
        if service == "control":
            other = {
                "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "level": "info",
                "service": service,
                "message": "other deployment noise",
                "trace_id": "other-trace",
                "request_id": "other-req",
                "forge.project": project,
                "forge.deployment": "dpl_other",
                "forge.service": service,
            }
            streams.append(
                {
                    "stream": {
                        "job": "forge-observe-itest",
                        "forge_project": project,
                        "forge_deployment": "dpl_other",
                        "forge_service": service,
                    },
                    "values": [[str(now_ns + 50_000_000), json.dumps(other)]],
                }
            )

    status, _ = http_json("POST", f"{loki.rstrip('/')}/loki/api/v1/push", {"streams": streams})
    if status >= 300:
        raise SystemExit(f"loki push failed: {status}")


def wait_query(observe: str, params: dict, predicate, attempts: int = 30) -> dict:
    qs = urllib.parse.urlencode(params)
    url = f"{observe.rstrip('/')}/v1/logs?{qs}"
    last = None
    for _ in range(attempts):
        try:
            status, body = http_json("GET", url)
        except urllib.error.HTTPError as e:
            raise SystemExit(f"observe query HTTP {e.code}: {e.read()!r}") from e
        if status != 200:
            raise SystemExit(f"observe query status {status}: {body}")
        last = body
        if predicate(body):
            return body
        time.sleep(0.5)
    raise SystemExit(f"query never matched predicate; last={last}")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--loki", required=True)
    ap.add_argument("--observe", required=True)
    args = ap.parse_args()

    project = "prj_itest_" + uuid.uuid4().hex[:8]
    deployment = "dpl_itest_1"
    trace_id = "trace_" + uuid.uuid4().hex
    request_id = "req_" + uuid.uuid4().hex[:12]

    push_logs(args.loki, project, deployment, trace_id, request_id)

    by_dep = wait_query(
        args.observe,
        {"project": project, "deployment": deployment, "limit": "50", "direction": "forward"},
        lambda b: len(b.get("entries") or []) >= 3
        and all(e.get("deployment") == deployment for e in b["entries"]),
    )
    services = {e.get("service") for e in by_dep["entries"]}
    if not {"control", "gateway", "runtime"}.issubset(services):
        raise SystemExit(f"expected multi-service deployment logs, got {services}")

    by_trace = wait_query(
        args.observe,
        {"trace_id": trace_id, "limit": "50", "direction": "forward"},
        lambda b: len(b.get("entries") or []) >= 3,
    )
    # Time-ordered ascending for direction=forward
    times = [e["time"] for e in by_trace["entries"]]
    if times != sorted(times):
        raise SystemExit(f"trace entries not time-ordered: {times}")
    trace_services = {e.get("service") for e in by_trace["entries"]}
    if len(trace_services) < 2:
        raise SystemExit(f"expected multi-service trace, got {trace_services}")

    print(
        json.dumps(
            {
                "ok": True,
                "project": project,
                "deployment": deployment,
                "trace_id": trace_id,
                "deployment_entries": len(by_dep["entries"]),
                "trace_entries": len(by_trace["entries"]),
                "trace_services": sorted(trace_services),
            }
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
