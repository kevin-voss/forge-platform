#!/usr/bin/env python3
"""OrderPipe fulfillment — health + stub fulfill endpoint (epic 54.01)."""

from __future__ import annotations

import json
import os
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse

PORT = int(os.environ.get("PORT", "8080"))
SERVICE_NAME = os.environ.get("FORGE_SERVICE_NAME", "orderpipe-fulfillment")
STARTED_AT = time.time()
_FULFILLMENTS: list[dict[str, Any]] = []


class Handler(BaseHTTPRequestHandler):
    server_version = "orderpipe-fulfillment/0.1"

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        return

    def do_OPTIONS(self) -> None:  # noqa: N802
        self.send_response(204)
        self._cors()
        self.end_headers()

    def do_GET(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/health/ready":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/fulfillments":
            self._write_json(200, {"items": list(_FULFILLMENTS)})
            return
        if path == "/" or path.startswith("/?"):
            self._write_json(
                200,
                {
                    "service": SERVICE_NAME,
                    "language": "python",
                    "status": "running",
                    "uptime_seconds": time.time() - STARTED_AT,
                    "fulfill": "POST /fulfill (stub until 54.04/54.05)",
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def do_POST(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path != "/fulfill":
            self._write_json(404, {"error": "not_found"})
            return
        body = self._read_json()
        if body is None:
            self._write_json(400, {"error": "invalid json"})
            return
        order_id = str(body.get("orderId") or body.get("order_id") or "").strip()
        if not order_id:
            self._write_json(400, {"error": "orderId is required"})
            return
        note = {
            "id": f"ffl-{uuid.uuid4().hex[:12]}",
            "orderId": order_id,
            "status": "accepted",
        }
        _FULFILLMENTS.append(note)
        self._write_json(202, note)

    def _read_json(self) -> dict[str, Any] | None:
        length = int(self.headers.get("Content-Length") or "0")
        raw = self.rfile.read(length) if length > 0 else b"{}"
        try:
            data = json.loads(raw.decode() or "{}")
        except json.JSONDecodeError:
            return None
        return data if isinstance(data, dict) else None

    def _cors(self) -> None:
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")

    def _write_json(self, status: int, payload: dict[str, Any] | list[Any]) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self._cors()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> None:
    print(f"{SERVICE_NAME} listening on :{PORT}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
