#!/usr/bin/env python3
"""Unit tests for PulseBoard Observe metrics sidecar + PromQL surfacing (55.04)."""

from __future__ import annotations

import importlib.util
import json
import threading
import urllib.error
import urllib.parse
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SERVER_PATH = ROOT / "metrics" / "server.py"


def _load_server():
    spec = importlib.util.spec_from_file_location("demo55_metrics", SERVER_PATH)
    mod = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(mod)
    return mod


def _http_json(method: str, url: str, body: dict | None = None) -> tuple[int, dict | list | None]:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        url,
        data=data,
        method=method,
        headers={"content-type": "application/json"} if body is not None else {},
    )
    try:
        with urllib.request.urlopen(req, timeout=3) as resp:
            raw = resp.read()
            if not raw:
                return resp.status, None
            return resp.status, json.loads(raw.decode())
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        payload = json.loads(raw.decode()) if raw else None
        return exc.code, payload


def main() -> None:
    mod = _load_server()
    with mod.LOCK:
        mod.APPLICATIONS.clear()
        mod.QUEUES.clear()

    httpd = ThreadingHTTPServer(("127.0.0.1", 0), mod.Handler)
    port = httpd.server_address[1]
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    base = f"http://127.0.0.1:{port}"

    try:
        code, body = _http_json(
            "PUT",
            f"{base}/demo/application/pulseboard-api",
            {
                "requestsPerSecond": 80,
                "replicas": 4,
                "p95LatencySeconds": 0.12,
                "sampleCount": 1500,
            },
        )
        assert code == 200, (code, body)
        assert body["replicas"] == 4
        assert body["requestsPerSecond"] == 80

        # Merge preserves replicas when loadgen refreshes RPS only.
        code, body = _http_json(
            "PUT",
            f"{base}/demo/application/pulseboard-api",
            {"requestsPerSecond": 90, "sampleCount": 1600},
        )
        assert code == 200, (code, body)
        assert body["replicas"] == 4
        assert body["requestsPerSecond"] == 90

        q_replicas = 'sum(forge_replicas_ready{application="pulseboard-api"})'
        code, body = _http_json(
            "GET",
            f"{base}/api/v1/query?query={urllib.parse.quote(q_replicas)}",
        )
        assert code == 200, (code, body)
        assert body["status"] == "success"
        assert float(body["data"]["result"][0]["value"][1]) == 4.0

        q_rps = 'sum(rate(forge_http_requests_total{application="pulseboard-api"}[1m]))'
        code, body = _http_json(
            "GET",
            f"{base}/v1/metrics/query?query={urllib.parse.quote(q_rps)}",
        )
        assert code == 200, (code, body)
        assert body["samples"][0]["value"] == 90.0

        q_p95 = (
            'histogram_quantile(0.95, sum(rate('
            'forge_http_request_duration_seconds_bucket{application="pulseboard-api"}[5m])) by (le))'
        )
        code, body = _http_json(
            "GET",
            f"{base}/api/v1/query?query={urllib.parse.quote(q_p95)}",
        )
        assert code == 200, (code, body)
        assert abs(float(body["data"]["result"][0]["value"][1]) - 0.12) < 1e-9

        # /metrics scrape text exposes the same series Grafana reads.
        with urllib.request.urlopen(f"{base}/metrics", timeout=3) as resp:
            text = resp.read().decode()
        assert 'forge_replicas_ready{application="pulseboard-api"' in text
        assert "4.0" in text or " 4" in text

        # Dashboard vs Observe consistency within tolerance.
        dash_replicas = 4
        observe_replicas = float(
            _http_json(
                "GET",
                f"{base}/api/v1/query?query={urllib.parse.quote(q_replicas)}",
            )[1]["data"]["result"][0]["value"][1]
        )
        assert abs(dash_replicas - observe_replicas) <= 0.5

        print("test_observe_surfacing: ok")
    finally:
        httpd.shutdown()


if __name__ == "__main__":
    main()
