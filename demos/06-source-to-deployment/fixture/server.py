#!/usr/bin/env python3
"""Minimal HTTP app for the source-to-deployment demo fixture."""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


PORT = int(os.environ.get("PORT", "8080"))


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt: str, *args) -> None:
        return

    def do_GET(self) -> None:  # noqa: N802
        if self.path in ("/health/live", "/health/ready"):
            body = b'{"status":"ok"}\n'
        else:
            body = (
                json.dumps(
                    {
                        "service": "source-to-deployment",
                        "ok": True,
                    }
                )
                + "\n"
            ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
