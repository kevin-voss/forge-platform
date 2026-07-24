#!/usr/bin/env python3
"""Events choreography unit tests for orderpipe-fulfillment (epic 54.04)."""

from __future__ import annotations

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

import events as events_mod
import server


class _FakeEventsHandler(BaseHTTPRequestHandler):
    published: list[dict] = []
    queue: list[dict] = []

    def log_message(self, fmt: str, *args) -> None:  # noqa: A003, ARG002
        return

    def do_GET(self) -> None:  # noqa: N802
        if urlparse(self.path).path == "/health/ready":
            self._json(200, {"status": "ok"})
            return
        self._json(404, {"error": "not_found"})

    def do_POST(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        length = int(self.headers.get("Content-Length") or "0")
        raw = self.rfile.read(length) if length else b"{}"
        body = json.loads(raw.decode() or "{}")
        if path == "/v1/consumers":
            self._json(201, {"name": body.get("name")})
            return
        if path == "/v1/events":
            _FakeEventsHandler.published.append(body)
            self._json(202, {"event_id": "evt-1"})
            return
        if path == "/v1/consume":
            msgs = []
            if _FakeEventsHandler.queue:
                msgs.append(_FakeEventsHandler.queue.pop(0))
            self._json(200, {"messages": msgs})
            return
        if path in ("/v1/processed", "/v1/ack", "/v1/nak"):
            self.send_response(204)
            self.end_headers()
            return
        self._json(404, {"error": "not_found"})

    def _json(self, status: int, payload: dict) -> None:
        raw = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


class EventsTests(unittest.TestCase):
    def setUp(self) -> None:
        _FakeEventsHandler.published = []
        _FakeEventsHandler.queue = []
        server._FULFILLMENTS.clear()
        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), _FakeEventsHandler)
        self.port = self.httpd.server_address[1]
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()

    def test_charged_event_publishes_fulfilled(self) -> None:
        cfg = events_mod.load_events_config(
            {
                "FORGE_EVENTS_URL": f"http://127.0.0.1:{self.port}",
                "FORGE_SERVICE_NAME": "orderpipe-fulfillment",
                "FORGE_EVENTS_CONSUMER": "orderpipe-fulfill",
                "FORGE_EVENTS_SUBJECT": "order.charged",
                "FORGE_EVENTS_PUBLISH_SUBJECT": "order.fulfilled",
            }
        )
        client = events_mod.EventsClient(cfg)
        client.ensure_consumer()
        msg = events_mod.DeliveredMessage(
            event_id="e1",
            subject="order.charged",
            ack_token="a1",
            delivery_count=1,
            data={
                "order_id": "ord-1",
                "customer_email": "buyer@example.com",
                "status": "charged",
                "total_cents": 1800,
                "occurred_at": "2026-07-24T10:00:00Z",
            },
        )
        server.handle_charged_event(client, msg)
        self.assertTrue(any(f["orderId"] == "ord-1" for f in server._FULFILLMENTS))
        self.assertEqual(len(_FakeEventsHandler.published), 1)
        pub = _FakeEventsHandler.published[0]
        self.assertEqual(pub["subject"], "order.fulfilled")
        self.assertEqual(pub["data"]["order_id"], "ord-1")
        self.assertEqual(pub["data"]["status"], "fulfilled")


if __name__ == "__main__":
    unittest.main()
