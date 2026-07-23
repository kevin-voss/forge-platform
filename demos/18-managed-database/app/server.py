#!/usr/bin/env python3
"""Demo app for managed PostgreSQL injection (epic 18).

Reads DATABASE_URL from the environment (injected by Forge Secrets/Runtime),
runs a tiny migration, writes a fixture row, and exposes read/verify endpoints.
Never hardcodes credentials.
"""

from __future__ import annotations

import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse

import psycopg

PORT = int(os.environ.get("PORT", "8080"))
SERVICE_NAME = os.environ.get("FORGE_SERVICE_NAME", "demo-managed-db")
SERVICE_VERSION = os.environ.get("FORGE_SERVICE_VERSION", "0.1.0")
FIXTURE_KEY = os.environ.get("FORGE_DEMO_FIXTURE_KEY", "demo18-fixture")
FIXTURE_VALUE = os.environ.get("FORGE_DEMO_FIXTURE_VALUE", "managed-db-ok")
STARTED_AT = time.time()

_READY = False
_READY_ERROR = ""


def database_url(environ: dict[str, str] | None = None) -> str:
    env = environ if environ is not None else os.environ
    return (env.get("DATABASE_URL") or "").strip()


def migrate_and_seed(url: str, key: str = FIXTURE_KEY, value: str = FIXTURE_VALUE) -> None:
    """Create fixture table and upsert the known demo row."""
    if not url:
        raise RuntimeError("DATABASE_URL is required")
    # Reject accidental hardcoding of Control's own DB.
    if "postgres:5432/forge" in url or ":5001/forge" in url:
        raise RuntimeError("refusing Control database URL")
    with psycopg.connect(url) as conn:
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS demo_fixture (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
            )
            """
        )
        conn.execute(
            """
            INSERT INTO demo_fixture(key, value)
            VALUES (%s, %s)
            ON CONFLICT (key) DO UPDATE
            SET value = EXCLUDED.value, updated_at = NOW()
            """,
            (key, value),
        )
        conn.commit()


def read_fixture(url: str, key: str = FIXTURE_KEY) -> str | None:
    with psycopg.connect(url) as conn:
        row = conn.execute(
            "SELECT value FROM demo_fixture WHERE key = %s",
            (key,),
        ).fetchone()
        return None if row is None else str(row[0])


def delete_fixture(url: str, key: str = FIXTURE_KEY) -> None:
    with psycopg.connect(url) as conn:
        conn.execute("DELETE FROM demo_fixture WHERE key = %s", (key,))
        conn.commit()


def status_payload(environ: dict[str, str] | None = None) -> dict[str, Any]:
    env = environ if environ is not None else os.environ
    url = database_url(env)
    present = bool(url)
    # Never echo the URL or credentials.
    payload: dict[str, Any] = {
        "DATABASE_URL_present": present,
        "ready": _READY if environ is None else present,
        "fixture_key": FIXTURE_KEY,
    }
    if present and environ is None and _READY:
        try:
            payload["fixture_value"] = read_fixture(url)
        except Exception as exc:  # noqa: BLE001 — surface readiness detail
            payload["fixture_error"] = type(exc).__name__
    blob = json.dumps(payload)
    if url and url in blob:
        raise AssertionError("DATABASE_URL leaked into status payload")
    return payload


def bootstrap() -> None:
    global _READY, _READY_ERROR
    url = database_url()
    if not url:
        _READY = False
        _READY_ERROR = "DATABASE_URL missing"
        return
    try:
        migrate_and_seed(url)
        value = read_fixture(url)
        if value != FIXTURE_VALUE:
            raise RuntimeError(f"fixture mismatch: {value!r}")
        _READY = True
        _READY_ERROR = ""
    except Exception as exc:  # noqa: BLE001
        _READY = False
        _READY_ERROR = f"{type(exc).__name__}: {exc}"


class Handler(BaseHTTPRequestHandler):
    server_version = "demo-managed-db/0.1"

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        return

    def do_GET(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/health/ready":
            if _READY:
                self._write_json(200, {"status": "ok"})
            else:
                self._write_json(503, {"status": "not_ready", "error": _READY_ERROR})
            return
        if path == "/db-status":
            self._write_json(200, status_payload())
            return
        if path == "/fixture":
            if not _READY:
                self._write_json(503, {"error": "not_ready", "detail": _READY_ERROR})
                return
            value = read_fixture(database_url())
            self._write_json(200, {"key": FIXTURE_KEY, "value": value})
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
                    "DATABASE_URL_present": bool(database_url()),
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def do_POST(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/fixture/clear":
            if not database_url():
                self._write_json(503, {"error": "DATABASE_URL missing"})
                return
            delete_fixture(database_url())
            self._write_json(200, {"cleared": True, "key": FIXTURE_KEY})
            return
        if path == "/fixture/seed":
            if not database_url():
                self._write_json(503, {"error": "DATABASE_URL missing"})
                return
            migrate_and_seed(database_url())
            self._write_json(200, {"seeded": True, "key": FIXTURE_KEY, "value": FIXTURE_VALUE})
            return
        self._write_json(404, {"error": "not_found"})

    def _write_json(self, status: int, payload: dict[str, Any]) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(body.__len__()))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    bootstrap()
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
