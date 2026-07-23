#!/usr/bin/env python3
"""Smoke-check that Forge Grafana dashboards are provisioned (step 12.03)."""

from __future__ import annotations

import json
import subprocess
import sys
import time
import urllib.request

BASE = "http://127.0.0.1:3000"
AUTH = "Basic YWRtaW46YWRtaW4="  # admin:admin


def get(path: str):
    req = urllib.request.Request(
        BASE + path,
        headers={"Authorization": AUTH},
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.load(resp)


def main() -> int:
    deadline = time.time() + 60
    titles: list[str] = []
    while time.time() < deadline:
        try:
            hits = get("/api/search?query=Forge")
            titles = sorted(
                {
                    h.get("title")
                    for h in hits
                    if h.get("title") and h.get("type") == "dash-db"
                }
            )
            if titles == [
                "Forge Deployment",
                "Forge Platform",
                "Forge Runtime",
                "Forge Service",
            ]:
                break
        except Exception:
            pass
        time.sleep(2)
    else:
        print("dashboards not provisioned; saw:", titles, file=sys.stderr)
        return 1

    want = {
        "forge-platform": "Forge Platform",
        "forge-service": "Forge Service",
        "forge-deployment": "Forge Deployment",
        "forge-runtime": "Forge Runtime",
    }
    for uid, title in want.items():
        body = get(f"/api/dashboards/uid/{uid}")
        got = body.get("dashboard", {}).get("title")
        if got != title:
            print(f"uid {uid}: title={got!r} want {title!r}", file=sys.stderr)
            return 1

    logs = subprocess.check_output(
        ["docker", "logs", "forge-grafana"],
        stderr=subprocess.STDOUT,
        text=True,
        errors="replace",
    )
    for bad in (
        "failed to load dashboard",
        "Dashboard title cannot be empty",
        "invalid character",
    ):
        if bad.lower() in logs.lower():
            print("grafana provisioning error:", bad, file=sys.stderr)
            return 1

    print("Dashboard provisioning checks passed:", ", ".join(titles))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
