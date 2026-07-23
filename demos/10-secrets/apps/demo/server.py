#!/usr/bin/env python3
"""Contract-compliant demo app for secrets injection (epic 10).

Exposes /, /health/live, /health/ready, and /secret-status.
/secret-status reports whether DATABASE_PASSWORD is present and its length —
never the plaintext value.
"""

from __future__ import annotations

import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse


PORT = int(os.environ.get("PORT", "8080"))
SERVICE_NAME = os.environ.get("FORGE_SERVICE_NAME", "demo-secrets")
SERVICE_VERSION = os.environ.get("FORGE_SERVICE_VERSION", "0.1.0")
STARTED_AT = time.time()


def secret_status_payload(environ: dict[str, str] | None = None) -> dict[str, Any]:
    """Build /secret-status body. Never includes the secret value."""
    env = environ if environ is not None else os.environ
    present = "DATABASE_PASSWORD" in env and env.get("DATABASE_PASSWORD") is not None
    value = env.get("DATABASE_PASSWORD", "") if present else ""
    # Treat empty string as present-but-empty so length can prove rotation.
    payload: dict[str, Any] = {
        "DATABASE_PASSWORD_present": present,
        "value_length": len(value) if present else 0,
    }
    # Hard guarantee: never echo the secret under any key.
    assert "DATABASE_PASSWORD" not in payload
    assert value == "" or value not in json.dumps(payload)
    return payload


class Handler(BaseHTTPRequestHandler):
    server_version = "demo-secrets/0.1"

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        return

    def do_GET(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/health/ready":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/secret-status":
            self._write_json(200, secret_status_payload())
            return
        if path == "/" or path.startswith("/?"):
            self._write_json(
                200,
                {
                    "service": SERVICE_NAME,
                    "language": "python",
                    "status": "running",
                    "version": SERVICE_VERSION,
                    "uptime_seconds": time.time() - STARTED_AT,
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def _write_json(self, status: int, payload: dict[str, Any]) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
