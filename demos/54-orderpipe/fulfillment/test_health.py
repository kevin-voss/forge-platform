#!/usr/bin/env python3
"""Health + NetworkPolicy debug tests for orderpipe-fulfillment (epic 54.03)."""

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
    def setUp(self) -> None:
        server._READY = True

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

    def test_fulfill_stub_allowed_path(self) -> None:
        """order-api → fulfillment remain accepted (allowed NetworkPolicy pair)."""
        h = FakeHandler("POST", "/fulfill", b'{"orderId":"ord-1"}')
        h.do_POST()
        self.assertEqual(h._status, 202)
        payload = json.loads(h.wfile.getvalue().decode())
        self.assertEqual(payload["orderId"], "ord-1")
        self.assertEqual(payload["status"], "accepted")


class DeniedCallTests(unittest.TestCase):
    def setUp(self) -> None:
        self._prev = server._HTTP_JSON

    def tearDown(self) -> None:
        server._HTTP_JSON = self._prev

    def test_denied_call_reports_block(self) -> None:
        calls: list[tuple[str, str]] = []

        def fake(method: str, url: str, body=None, timeout: float = 3.0):  # noqa: ANN001, ARG001
            calls.append((method, url))
            if url.endswith("/network-policy-denied"):
                self.assertEqual(method, "POST")
                assert body is not None
                self.assertEqual(body["from_workload"], "wl-fulfillment")
                self.assertEqual(body["to_workload"], "wl-notify")
                return 202, {"status": "recorded", "event": "network.policy.denied"}
            self.fail(f"unexpected call {method} {url}")

        server._HTTP_JSON = fake
        h = FakeHandler(
            "POST",
            "/debug/denied-call",
            b'{"fromWorkload":"wl-fulfillment","toWorkload":"wl-notify"}',
        )
        h.do_POST()
        self.assertEqual(h._status, 403)
        payload = json.loads(h.wfile.getvalue().decode())
        self.assertTrue(payload["blocked"])
        self.assertEqual(payload["pair"], "fulfillment→notify")
        self.assertEqual(payload["event"], "network.policy.denied")
        self.assertFalse(payload["notifyAttempted"])
        self.assertEqual(len(calls), 1)

    def test_denied_call_requires_workloads(self) -> None:
        h = FakeHandler("POST", "/debug/denied-call", b"{}")
        h.do_POST()
        self.assertEqual(h._status, 400)
        payload = json.loads(h.wfile.getvalue().decode())
        self.assertTrue(payload["blocked"])


if __name__ == "__main__":
    unittest.main()
