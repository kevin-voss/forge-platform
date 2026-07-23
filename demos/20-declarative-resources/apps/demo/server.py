#!/usr/bin/env python3
"""Contract-compliant demo app for rolling deployment scenarios.

Exposes /, /health/live, /health/ready. VERSION is baked in at image build time.
When READY_FAIL=true (v3-broken), /health/ready always returns 503.
"""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


PORT = int(os.environ.get("PORT", "8080"))
VERSION = os.environ.get("VERSION", "v1")
READY_FAIL = os.environ.get("READY_FAIL", "false").strip().lower() in ("1", "true", "yes")


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt: str, *args) -> None:  # noqa: A003
        return

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if self.path == "/health/ready":
            if READY_FAIL:
                self._write_json(503, {"status": "not_ready", "version": VERSION})
            else:
                self._write_json(200, {"status": "ok"})
            return
        if self.path == "/" or self.path.startswith("/?"):
            self._write_json(
                200,
                {
                    "service": "rolling-deployment",
                    "version": VERSION,
                    "ok": True,
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def _write_json(self, status: int, payload: dict) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
