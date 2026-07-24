#!/usr/bin/env python3
"""Message persistence repository tests for AskDocs (epic 53.01)."""

from __future__ import annotations

import json
import os
import subprocess
import time
import unittest
import uuid
from pathlib import Path

from store import EmptyTextError, MessageStore, StoreError, resolve_migrations_dir


def _start_postgres() -> tuple[str, str]:
    """Start a throwaway Postgres container; return (dsn, container_id)."""
    name = f"askdocs-test-pg-{uuid.uuid4().hex[:8]}"
    subprocess.run(
        [
            "docker",
            "run",
            "-d",
            "--rm",
            "--name",
            name,
            "-e",
            "POSTGRES_PASSWORD=askdocs",
            "-e",
            "POSTGRES_USER=askdocs",
            "-e",
            "POSTGRES_DB=askdocs_test",
            "-p",
            "127.0.0.1::5432",
            "postgres:16-alpine",
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    port = subprocess.check_output(
        ["docker", "port", name, "5432"],
        text=True,
    ).strip().split(":")[-1]
    dsn = f"postgresql://askdocs:askdocs@127.0.0.1:{port}/askdocs_test"
    deadline = time.time() + 45
    last = ""
    while time.time() < deadline:
        try:
            store = MessageStore(dsn, resolve_migrations_dir())
            store.connect()
            store.close()
            return dsn, name
        except Exception as exc:  # noqa: BLE001
            last = str(exc)
            time.sleep(1)
    subprocess.run(["docker", "rm", "-f", name], check=False, capture_output=True)
    raise RuntimeError(f"postgres not ready: {last}")


class MessageStoreTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        dsn = (os.environ.get("ASKDOCS_TEST_DATABASE_URL") or "").strip()
        cls._container = ""
        if not dsn:
            try:
                dsn, cls._container = _start_postgres()
            except Exception as exc:  # noqa: BLE001
                raise unittest.SkipTest(f"postgres test container unavailable: {exc}") from exc
        cls.dsn = dsn
        cls.migrations = resolve_migrations_dir(
            str(Path(__file__).resolve().parent.parent / "migrations")
        )

    @classmethod
    def tearDownClass(cls) -> None:
        if cls._container:
            subprocess.run(
                ["docker", "rm", "-f", cls._container],
                check=False,
                capture_output=True,
            )

    def setUp(self) -> None:
        self.store = MessageStore(self.dsn, self.migrations)
        self.store.migrate()
        # Isolate each test in a unique session.
        self.session = f"sess-{uuid.uuid4().hex[:8]}"

    def tearDown(self) -> None:
        self.store.close()

    def test_requires_database_url(self) -> None:
        with self.assertRaises(StoreError):
            MessageStore("")

    def test_refuses_control_db(self) -> None:
        with self.assertRaises(StoreError):
            MessageStore("postgresql://forge:forge@postgres:5432/forge")

    def test_empty_text_rejected(self) -> None:
        with self.assertRaises(EmptyTextError):
            self.store.append_message(self.session, "user", "   ")

    def test_message_persistence_roundtrip(self) -> None:
        user = self.store.append_message(self.session, "user", "When is the office closed?")
        self.assertEqual(user.role, "user")
        self.assertTrue(user.id)
        assistant = self.store.append_message(
            self.session, "assistant", "Echo: When is the office closed?", citations=[]
        )
        listed = self.store.list_messages(self.session)
        self.assertEqual(len(listed), 2)
        self.assertEqual(listed[0].id, user.id)
        self.assertEqual(listed[1].id, assistant.id)
        self.assertEqual(listed[0].text, "When is the office closed?")
        self.assertEqual(listed[1].role, "assistant")

    def test_echo_chat_persists_pair(self) -> None:
        result = self.store.echo_chat(self.session, "hello askdocs")
        self.assertEqual(result["sessionId"], self.session)
        self.assertEqual(result["user"]["text"], "hello askdocs")
        self.assertEqual(result["assistant"]["text"], "Echo: hello askdocs")
        listed = self.store.list_messages(self.session)
        self.assertEqual(len(listed), 2)
        self.assertEqual(listed[0].role, "user")
        self.assertEqual(listed[1].role, "assistant")
        # API encodes Message.to_json(); datetimes must not leak into the payload.
        blob = json.dumps({"messages": [m.to_json() for m in listed]})
        self.assertIn("createdAt", blob)
        self.assertNotIn("created_at", blob)


if __name__ == "__main__":
    unittest.main()
