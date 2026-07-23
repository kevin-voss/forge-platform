#!/usr/bin/env python3
"""Demo 24 fake Gateway/Events admin metrics + event sink.

Serves the shapes forge-autoscaler expects:
  GET /admin/metrics?application=<name>
  GET /admin/metrics?queue=<name>

Traffic generator / queue publisher update state via:
  PUT /demo/application/<name>
  PUT /demo/queue/<name>
  DELETE /demo/application/<name>
"""

from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

LOCK = threading.Lock()
APPLICATIONS: dict[str, dict] = {}
QUEUES: dict[str, dict] = {}


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


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt: str, *args) -> None:  # quieter demo logs
        if self.path.startswith("/health"):
            return
        super().log_message(fmt, *args)

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        if parsed.path in ("/health/live", "/health/ready", "/health"):
            _write(self, 200, {"status": "ok"})
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
                APPLICATIONS[name] = {
                    "application": name,
                    "requestsPerSecond": float(body.get("requestsPerSecond", 0)),
                    "activeConnections": float(body.get("activeConnections", 0)),
                    "sampleCount": int(body.get("sampleCount", 1000)),
                    "p95LatencySeconds": float(body.get("p95LatencySeconds", 0.05)),
                    "errorRate": float(body.get("errorRate", 0.0)),
                }
                _write(self, 200, APPLICATIONS[name])
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
    print(f"demo24-metrics listening on {host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
