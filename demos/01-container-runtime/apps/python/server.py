"""HTTP handlers for the Forge runtime contract surface."""

from __future__ import annotations

import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse

from config import Config
from jsonlog import Logger


class ContractHandler(BaseHTTPRequestHandler):
    server_version = "demo-python-api/0.1"
    cfg: Config
    log: Logger
    started_at: float

    def log_message(self, format: str, *args: Any) -> None:  # noqa: A003
        # Suppress default access log; use structured JSON elsewhere.
        return

    def do_GET(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/health/ready":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/":
            self._write_json(
                200,
                {
                    "service": self.cfg.service_name,
                    "language": "python",
                    "status": "running",
                    "version": self.cfg.service_version,
                    "uptime_seconds": time.time() - self.started_at,
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def _write_json(self, status: int, payload: dict[str, Any]) -> None:
        body = (json.dumps(payload, separators=(",", ":")) + "\n").encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def make_server(cfg: Config, log: Logger) -> ThreadingHTTPServer:
    handler = type(
        "BoundContractHandler",
        (ContractHandler,),
        {
            "cfg": cfg,
            "log": log,
            "started_at": time.time(),
        },
    )
    return ThreadingHTTPServer(("0.0.0.0", cfg.port), handler)
