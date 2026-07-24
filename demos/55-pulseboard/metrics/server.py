#!/usr/bin/env python3
"""Demo 55 Observe metrics sidecar (Gateway admin + Prometheus-compatible Observe).

Serves the shapes forge-autoscaler and PulseBoard /stats expect:

  GET /admin/metrics?application=<name>
  GET /admin/metrics?queue=<name>
  GET /api/v1/query?query=<PromQL>          # Prometheus instant query
  GET /v1/metrics/query?query=<PromQL>      # Observe metrics facade
  GET /metrics                              # Prometheus scrape text

Traffic generator / Control sync update state via:

  PUT /demo/application/<name>
  PUT /demo/queue/<name>
  DELETE /demo/application/<name>
"""

from __future__ import annotations

import json
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

LOCK = threading.Lock()
APPLICATIONS: dict[str, dict] = {}
QUEUES: dict[str, dict] = {}

# PromQL fragments we honor for PulseBoard / autoscaler-style queries.
_RE_APP = re.compile(r'application\s*=\s*"([^"]+)"')
_RE_SVC = re.compile(r'(?:forge_service|service_name)\s*=\s*"([^"]+)"')
_RE_REPLICAS = re.compile(r"forge_replicas_ready(?:_total)?")
_RE_RPS = re.compile(r"rate\s*\(\s*forge_http_requests_total|requestsPerSecond")
_RE_P95 = re.compile(r"histogram_quantile\s*\(\s*0\.95|forge_http_request_duration_seconds")


def _read_json(handler: BaseHTTPRequestHandler) -> dict:
    length = int(handler.headers.get("Content-Length") or 0)
    if length <= 0:
        return {}
    raw = handler.rfile.read(length)
    if not raw:
        return {}
    return json.loads(raw.decode("utf-8"))


def _write(handler: BaseHTTPRequestHandler, code: int, body: dict | list | None = None) -> None:
    payload = b"" if body is None else json.dumps(body).encode("utf-8")
    handler.send_response(code)
    handler.send_header("content-type", "application/json")
    handler.send_header("content-length", str(len(payload)))
    handler.end_headers()
    if payload:
        handler.wfile.write(payload)


def _write_text(handler: BaseHTTPRequestHandler, code: int, body: str, content_type: str) -> None:
    payload = body.encode("utf-8")
    handler.send_response(code)
    handler.send_header("content-type", content_type)
    handler.send_header("content-length", str(len(payload)))
    handler.end_headers()
    handler.wfile.write(payload)


def _app_name(query: str) -> str | None:
    m = _RE_APP.search(query) or _RE_SVC.search(query)
    return m.group(1) if m else None


def _prom_vector(value: float, metric: dict[str, str] | None = None) -> dict:
    return {
        "status": "success",
        "data": {
            "resultType": "vector",
            "result": [
                {
                    "metric": metric or {},
                    "value": [time.time(), f"{value}"],
                }
            ],
        },
    }


def _observe_facade(query: str, value: float, metric: dict[str, str] | None = None) -> dict:
    return {
        "query": query,
        "result_type": "vector",
        "samples": [
            {
                "metric": metric or {},
                "timestamp": time.time(),
                "value": value,
            }
        ],
    }


def _resolve_query(query: str) -> tuple[float, dict[str, str]] | None:
    q = query.strip()
    name = _app_name(q)
    with LOCK:
        if name and name in APPLICATIONS:
            row = APPLICATIONS[name]
        elif len(APPLICATIONS) == 1 and name is None:
            name, row = next(iter(APPLICATIONS.items()))
        elif name is None and APPLICATIONS:
            # Prefer pulseboard-api when present.
            name = "pulseboard-api" if "pulseboard-api" in APPLICATIONS else next(iter(APPLICATIONS))
            row = APPLICATIONS[name]
        else:
            return None

        labels = {"application": name, "forge_service": name, "service_name": name}
        if _RE_REPLICAS.search(q):
            return float(row.get("replicas", 1)), labels
        if _RE_P95.search(q):
            return float(row.get("p95LatencySeconds", 0.0)), labels
        if _RE_RPS.search(q) or "forge_http_requests_total" in q:
            return float(row.get("requestsPerSecond", 0.0)), labels
    return None


def _metrics_text() -> str:
    lines = [
        "# HELP forge_replicas_ready Ready replica count for a Forge application.",
        "# TYPE forge_replicas_ready gauge",
        "# HELP forge_http_requests_total Total HTTP requests (demo counter proxy).",
        "# TYPE forge_http_requests_total counter",
        "# HELP forge_http_request_duration_seconds Request duration histogram (demo summary).",
        "# TYPE forge_http_request_duration_seconds summary",
    ]
    with LOCK:
        for name, row in APPLICATIONS.items():
            labels = f'application="{name}",forge_service="{name}",service_name="{name}"'
            replicas = float(row.get("replicas", 1))
            rps = float(row.get("requestsPerSecond", 0.0))
            p95 = float(row.get("p95LatencySeconds", 0.0))
            # Approximate a counter from RPS so rate() scrapes stay informative.
            sample_count = int(row.get("sampleCount", 1000))
            lines.append(f"forge_replicas_ready{{{labels}}} {replicas}")
            lines.append(f"forge_replicas_ready_total{{{labels}}} {replicas}")
            lines.append(f"forge_http_requests_total{{{labels},http_method=\"POST\",http_status_class=\"2xx\"}} {sample_count}")
            lines.append(f"forge_http_request_duration_seconds{{quantile=\"0.95\",{labels}}} {p95}")
            # Keep a synthetic bucket so histogram_quantile-style queries have a series.
            lines.append(
                f'forge_http_request_duration_seconds_bucket{{le="+Inf",{labels}}} {max(sample_count, 1)}'
            )
            lines.append(f"# demo_rps {name}={rps}")
    lines.append("")
    return "\n".join(lines)


def _merge_application(name: str, body: dict) -> dict:
    prev = APPLICATIONS.get(name) or {}
    row = {
        "application": name,
        "requestsPerSecond": float(body.get("requestsPerSecond", prev.get("requestsPerSecond", 0))),
        "activeConnections": float(body.get("activeConnections", prev.get("activeConnections", 0))),
        "sampleCount": int(body.get("sampleCount", prev.get("sampleCount", 1000))),
        "p95LatencySeconds": float(body.get("p95LatencySeconds", prev.get("p95LatencySeconds", 0.05))),
        "errorRate": float(body.get("errorRate", prev.get("errorRate", 0.0))),
        "replicas": int(body.get("replicas", prev.get("replicas", 1))),
    }
    APPLICATIONS[name] = row
    return row


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt: str, *args) -> None:  # quieter demo logs
        if self.path.startswith("/health") or self.path.startswith("/metrics"):
            return
        super().log_message(fmt, *args)

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        if parsed.path in ("/health/live", "/health/ready", "/health"):
            _write(self, 200, {"status": "ok"})
            return
        if parsed.path == "/metrics":
            _write_text(self, 200, _metrics_text(), "text/plain; version=0.0.4; charset=utf-8")
            return
        if parsed.path in ("/api/v1/query", "/v1/metrics/query"):
            qs = parse_qs(parsed.query)
            query = (qs.get("query") or [""])[0]
            resolved = _resolve_query(query)
            if resolved is None:
                empty = {
                    "status": "success",
                    "data": {"resultType": "vector", "result": []},
                }
                if parsed.path == "/v1/metrics/query":
                    _write(self, 200, {"query": query, "result_type": "vector", "samples": []})
                    return
                _write(self, 200, empty)
                return
            value, labels = resolved
            if parsed.path == "/v1/metrics/query":
                _write(self, 200, _observe_facade(query, value, labels))
                return
            _write(self, 200, _prom_vector(value, labels))
            return
        if parsed.path == "/admin/metrics":
            qs = parse_qs(parsed.query)
            app = (qs.get("application") or [None])[0]
            queue = (qs.get("queue") or [None])[0]
            with LOCK:
                if app:
                    row = APPLICATIONS.get(app)
                    if row is None:
                        _write(self, 404, {"error": "application metrics missing", "application": app})
                        return
                    _write(self, 200, row)
                    return
                if queue:
                    row = QUEUES.get(queue)
                    if row is None:
                        _write(self, 404, {"error": "queue metrics missing", "queue": queue})
                        return
                    _write(self, 200, row)
                    return
            _write(self, 400, {"error": "application or queue query required"})
            return
        _write(self, 404, {"error": "not found", "path": parsed.path})

    def do_PUT(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        parts = [p for p in parsed.path.split("/") if p]
        body = _read_json(self)
        with LOCK:
            if len(parts) == 3 and parts[0] == "demo" and parts[1] == "application":
                name = parts[2]
                _write(self, 200, _merge_application(name, body))
                return
            if len(parts) == 3 and parts[0] == "demo" and parts[1] == "queue":
                name = parts[2]
                QUEUES[name] = {
                    "queue": name,
                    "depth": float(body.get("depth", 0)),
                    "oldestAgeSeconds": float(body.get("oldestAgeSeconds", 0)),
                    "consumerLag": float(body.get("consumerLag", 0)),
                    "retryRate": float(body.get("retryRate", 0)),
                    "processingDurationSeconds": float(body.get("processingDurationSeconds", 0.5)),
                    "deadLetterCount": int(body.get("deadLetterCount", 0)),
                }
                _write(self, 200, QUEUES[name])
                return
        _write(self, 404, {"error": "not found", "path": parsed.path})

    def do_DELETE(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        parts = [p for p in parsed.path.split("/") if p]
        with LOCK:
            if len(parts) == 3 and parts[0] == "demo" and parts[1] == "application":
                APPLICATIONS.pop(parts[2], None)
                _write(self, 204, None)
                return
            if len(parts) == 3 and parts[0] == "demo" and parts[1] == "queue":
                QUEUES.pop(parts[2], None)
                _write(self, 204, None)
                return
        _write(self, 404, {"error": "not found", "path": parsed.path})

    def do_POST(self) -> None:  # noqa: N802
        # Accept audit publishes so FORGE_EVENTS_URL can point here.
        parsed = urlparse(self.path)
        if parsed.path.startswith("/v1/events") or parsed.path == "/v1/publish":
            _ = _read_json(self)
            _write(self, 202, {"status": "accepted"})
            return
        _write(self, 404, {"error": "not found", "path": parsed.path})


def main() -> None:
    host, port = "0.0.0.0", 8080
    server = ThreadingHTTPServer((host, port), Handler)
    print(f"demo55-metrics (Observe) listening on {host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
