#!/usr/bin/env python3
"""Dev-only Alertmanager webhook sink — logs firing/resolved payloads as JSON."""

from __future__ import annotations

import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class WebhookHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        if self.path in ("/health", "/health/live", "/"):
            self._write(200, b'{"status":"ok"}\n')
            return
        self._write(404, b'{"error":"not_found"}\n')

    def do_POST(self) -> None:  # noqa: N802
        if self.path.rstrip("/") != "/webhook":
            self._write(404, b'{"error":"not_found"}\n')
            return
        length = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(length) if length > 0 else b"{}"
        try:
            payload = json.loads(raw.decode("utf-8") or "{}")
        except json.JSONDecodeError:
            payload = {"raw": raw.decode("utf-8", errors="replace")}

        status = payload.get("status", "unknown")
        alerts = payload.get("alerts") or []
        names = sorted(
            {
                str((a.get("labels") or {}).get("alertname", ""))
                for a in alerts
                if isinstance(a, dict)
            }
        )
        line = {
            "event": "alertmanager_webhook",
            "status": status,
            "alert_count": len(alerts) if isinstance(alerts, list) else 0,
            "alertnames": [n for n in names if n],
            "payload": payload,
        }
        print(json.dumps(line, separators=(",", ":")), flush=True)
        self._write(200, b'{"status":"ok"}\n')

    def log_message(self, fmt: str, *args) -> None:  # noqa: A003
        # Access logs stay quiet; alert payloads are the useful signal.
        return

    def _write(self, code: int, body: bytes) -> None:
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> int:
    server = ThreadingHTTPServer(("0.0.0.0", 8080), WebhookHandler)
    print(
        json.dumps({"event": "webhook_sink_listen", "addr": "0.0.0.0:8080"}),
        flush=True,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
