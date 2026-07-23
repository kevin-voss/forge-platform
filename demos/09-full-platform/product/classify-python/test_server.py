"""Unit tests for incident-classify handlers, config, and determinism."""

from __future__ import annotations

import json
import os
import threading
import time
import unittest
import urllib.request
from unittest import mock

from classify import classify_text
from config import Config, load_config
from jsonlog import Logger
from server import make_server


class _LiveServer:
    def __init__(self) -> None:
        cfg = Config(
            port=0,
            service_name="incident-classify",
            service_version="0.1.0",
            log_level="error",
            env="test",
        )
        self.httpd = make_server(cfg, Logger("incident-classify", "error"))
        handler_cls = self.httpd.RequestHandlerClass
        handler_cls.started_at = time.time() - 2.0  # type: ignore[attr-defined]
        self.port = self.httpd.server_address[1]
        self._thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    def __enter__(self) -> "_LiveServer":
        self._thread.start()
        return self

    def __exit__(self, *args: object) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()
        self._thread.join(timeout=5)

    def get(self, path: str) -> tuple[int, dict, str]:
        url = f"http://127.0.0.1:{self.port}{path}"
        req = urllib.request.Request(url, method="GET")
        with urllib.request.urlopen(req, timeout=2) as resp:
            body = resp.read()
            ct = resp.headers.get("Content-Type", "")
            return resp.status, json.loads(body.decode()), ct

    def post(self, path: str, payload: dict) -> tuple[int, dict]:
        url = f"http://127.0.0.1:{self.port}{path}"
        data = json.dumps(payload).encode("utf-8")
        req = urllib.request.Request(
            url,
            data=data,
            method="POST",
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=2) as resp:
            return resp.status, json.loads(resp.read().decode())


class TestHealthEndpoints(unittest.TestCase):
    def test_live_and_ready(self) -> None:
        with _LiveServer() as srv:
            for path in ("/health/live", "/health/ready"):
                status, body, ct = srv.get(path)
                self.assertEqual(status, 200)
                self.assertEqual(ct, "application/json")
                self.assertEqual(body, {"status": "ok"})


class TestIdentityEndpoint(unittest.TestCase):
    def test_identity(self) -> None:
        with _LiveServer() as srv:
            status, body, ct = srv.get("/")
            self.assertEqual(status, 200)
            self.assertEqual(ct, "application/json")
            self.assertEqual(body["service"], "incident-classify")
            self.assertEqual(body["language"], "python")
            self.assertEqual(body["status"], "running")
            self.assertGreater(body["uptime_seconds"], 0)


class TestClassificationDeterminism(unittest.TestCase):
    def test_keyword_and_stable_hash(self) -> None:
        a = classify_text("Deploy rollback failed after canary")
        b = classify_text("Deploy rollback failed after canary")
        self.assertEqual(a, b)
        self.assertEqual(a.label, "deployment")

        c = classify_text("mysterious orange widget")
        d = classify_text("mysterious orange widget")
        self.assertEqual(c, d)
        self.assertEqual(c.reason, "stable_hash")

    def test_http_classify(self) -> None:
        with _LiveServer() as srv:
            status, body = srv.post("/classify", {"text": "OOM killed the pod"})
            self.assertEqual(status, 200)
            self.assertEqual(body["label"], "resource_exhaustion")


class TestLoadConfig(unittest.TestCase):
    def test_requires_port(self) -> None:
        with mock.patch.dict(os.environ, {"FORGE_LOG_LEVEL": "info"}, clear=True):
            with self.assertRaises(ValueError):
                load_config()

    def test_defaults(self) -> None:
        with mock.patch.dict(os.environ, {"PORT": "8080"}, clear=True):
            cfg = load_config()
        self.assertEqual(cfg.service_name, "incident-classify")


if __name__ == "__main__":
    unittest.main()
