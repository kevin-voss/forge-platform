#!/usr/bin/env python3
"""Health tests for orderpipe-fulfillment (epic 54.01)."""

from __future__ import annotations

import json
import unittest
from io import BytesIO

import server


class FakeHandler(server.Handler):
    def __init__(self, method: str, path: str, body: bytes = b"") -> None:
        self.requestline = f"{method} {path} HTTP/1.1"
        self.command = method
        self.path = path
        self.headers = {"Content-Length": str(len(body))}
        self.rfile = BytesIO(body)
        self.wfile = BytesIO()
        self._status: int | None = None
        self._headers: list[tuple[str, str]] = []

    def send_response(self, code: int, message: str | None = None) -> None:  # noqa: ARG002
        self._status = code

    def send_header(self, keyword: str, value: str) -> None:
        self._headers.append((keyword, value))

    def end_headers(self) -> None:
        return


class HealthTests(unittest.TestCase):
    def test_ready(self) -> None:
        h = FakeHandler("GET", "/health/ready")
        h.do_GET()
        self.assertEqual(h._status, 200)
        payload = json.loads(h.wfile.getvalue().decode())
        self.assertEqual(payload["status"], "ok")

    def test_live(self) -> None:
        h = FakeHandler("GET", "/health/live")
        h.do_GET()
        self.assertEqual(h._status, 200)

    def test_fulfill_stub(self) -> None:
        h = FakeHandler("POST", "/fulfill", b'{"orderId":"ord-1"}')
        h.do_POST()
        self.assertEqual(h._status, 202)
        payload = json.loads(h.wfile.getvalue().decode())
        self.assertEqual(payload["orderId"], "ord-1")
        self.assertEqual(payload["status"], "accepted")


if __name__ == "__main__":
    unittest.main()
