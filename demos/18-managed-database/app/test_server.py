#!/usr/bin/env python3
"""Unit tests for demo 18 migrate/write/read against a DATABASE_URL."""

from __future__ import annotations

import json
import os
import unittest
from unittest import mock

from server import (
    FIXTURE_KEY,
    FIXTURE_VALUE,
    database_url,
    delete_fixture,
    migrate_and_seed,
    read_fixture,
    status_payload,
)


class StatusPayloadTests(unittest.TestCase):
    def test_absent(self) -> None:
        payload = status_payload({})
        self.assertFalse(payload["DATABASE_URL_present"])
        self.assertNotIn("postgresql://", json.dumps(payload))

    def test_present_does_not_echo_url(self) -> None:
        url = "postgresql://user:s3cret@db:5432/app"
        payload = status_payload({"DATABASE_URL": url})
        self.assertTrue(payload["DATABASE_URL_present"])
        blob = json.dumps(payload)
        self.assertNotIn(url, blob)
        self.assertNotIn("s3cret", blob)


class MigrationTests(unittest.TestCase):
    def test_requires_url(self) -> None:
        with self.assertRaises(RuntimeError):
            migrate_and_seed("")

    def test_refuses_control_db(self) -> None:
        with self.assertRaises(RuntimeError):
            migrate_and_seed("postgresql://forge:forge@postgres:5432/forge")

    def test_migrate_write_read_roundtrip(self) -> None:
        url = os.environ.get("DEMO18_TEST_DATABASE_URL", "").strip()
        if not url:
            self.skipTest("DEMO18_TEST_DATABASE_URL not set")
        migrate_and_seed(url, key=FIXTURE_KEY, value=FIXTURE_VALUE)
        self.assertEqual(read_fixture(url, FIXTURE_KEY), FIXTURE_VALUE)
        delete_fixture(url, FIXTURE_KEY)
        self.assertIsNone(read_fixture(url, FIXTURE_KEY))
        migrate_and_seed(url, key=FIXTURE_KEY, value=FIXTURE_VALUE)
        self.assertEqual(read_fixture(url, FIXTURE_KEY), FIXTURE_VALUE)

    def test_database_url_helper(self) -> None:
        with mock.patch.dict(os.environ, {"DATABASE_URL": " postgresql://x "}, clear=False):
            self.assertEqual(database_url(), "postgresql://x")


if __name__ == "__main__":
    unittest.main()
