#!/usr/bin/env python3
"""AskDocs API — chat echo stub + Postgres message persistence (epic 53.01)."""

from __future__ import annotations

import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs, urlparse

from store import EmptyTextError, MessageStore, StoreError, open_store_with_retry

PORT = int(os.environ.get("PORT", "8080"))
SERVICE_NAME = os.environ.get("FORGE_SERVICE_NAME", "askdocs-api")
DEFAULT_SESSION = os.environ.get("ASKDOCS_DEFAULT_SESSION", "default")
STARTED_AT = time.time()

_STORE: MessageStore | None = None


def get_store() -> MessageStore:
    global _STORE
    if _STORE is None:
        raise StoreError("store not initialized")
    return _STORE


class Handler(BaseHTTPRequestHandler):
    server_version = "askdocs-api/0.1"

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        return

    def do_OPTIONS(self) -> None:  # noqa: N802
        self.send_response(204)
        self._cors()
        self.end_headers()

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        path = parsed.path
        if path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/health/ready":
            try:
                get_store().ping()
                self._write_json(200, {"status": "ok"})
            except Exception as exc:  # noqa: BLE001
                self._write_json(
                    503,
                    {"status": "not_ready", "error": f"{type(exc).__name__}: {exc}"},
                )
            return
        if path == "/messages":
            qs = parse_qs(parsed.query)
            session_id = (qs.get("sessionId") or qs.get("session_id") or [DEFAULT_SESSION])[0]
            try:
                messages = [m.to_json() for m in get_store().list_messages(session_id)]
                self._write_json(200, {"sessionId": session_id, "messages": messages})
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"list failed: {exc}"})
            return
        if path == "/documents":
            # Stub until 53.02 upload/ingest.
            self._write_json(200, {"documents": []})
            return
        if path == "/" or path.startswith("/?"):
            self._write_json(
                200,
                {
                    "service": SERVICE_NAME,
                    "language": "python",
                    "status": "running",
                    "uptime_seconds": time.time() - STARTED_AT,
                    "chat": "POST /chat (echo stub until 53.04)",
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def do_POST(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        path = parsed.path
        if path == "/chat":
            body = self._read_json()
            if body is None:
                self._write_json(400, {"error": "invalid json"})
                return
            text = str(body.get("text") or body.get("message") or "")
            session_id = str(
                body.get("sessionId") or body.get("session_id") or DEFAULT_SESSION
            )
            try:
                result = get_store().echo_chat(session_id, text)
                self._write_json(201, result)
            except EmptyTextError:
                self._write_json(400, {"error": "text is required"})
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"chat failed: {exc}"})
            return
        self._write_json(404, {"error": "not_found"})

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
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization")

    def _write_json(self, status: int, payload: dict[str, Any] | list[Any]) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self._cors()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> None:
    global _STORE
    store = open_store_with_retry()
    store.migrate()
    _STORE = store
    print(f"askdocs-api migrations applied from {store.migrations_dir}", flush=True)
    print(f"askdocs-api listening on :{PORT}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
