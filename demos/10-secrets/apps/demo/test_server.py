#!/usr/bin/env python3
"""Tiny unit tests for /secret-status presence-check (never returns values)."""

from __future__ import annotations

import json
import unittest

from server import secret_status_payload


class SecretStatusTests(unittest.TestCase):
    def test_present_reports_length_not_value(self) -> None:
        payload = secret_status_payload({"DATABASE_PASSWORD": "pw1"})
        self.assertTrue(payload["DATABASE_PASSWORD_present"])
        self.assertEqual(payload["value_length"], 3)
        self.assertEqual(set(payload), {"DATABASE_PASSWORD_present", "value_length"})
        self.assertNotIn("pw1", json.dumps(payload))

    def test_absent(self) -> None:
        payload = secret_status_payload({})
        self.assertFalse(payload["DATABASE_PASSWORD_present"])
        self.assertEqual(payload["value_length"], 0)

    def test_rotation_length(self) -> None:
        payload = secret_status_payload({"DATABASE_PASSWORD": "pw-longer"})
        self.assertEqual(payload["value_length"], 9)
        self.assertNotIn("pw-longer", json.dumps(payload))


if __name__ == "__main__":
    unittest.main()
