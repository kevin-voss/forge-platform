"""Unit tests for foundations helpers (19.03)."""

from __future__ import annotations

import json
import unittest

from foundations_helpers import (
    assert_no_plaintext,
    db_status_ok,
    mask_secrets_in_text,
    secret_status_ok,
    tempo_service_names,
)


class FoundationsHelpersTest(unittest.TestCase):
    def test_mask_secrets(self) -> None:
        text = "url=postgresql://u:p@h/db tok=forge_pat_abc123 extra=xyz"
        masked = mask_secrets_in_text(text, ["xyz"])
        self.assertNotIn("postgresql://", masked)
        self.assertNotIn("forge_pat_", masked)
        self.assertNotIn("xyz", masked)
        self.assertIn("***", masked)

    def test_assert_no_plaintext(self) -> None:
        assert_no_plaintext("safe", ["secret"])
        with self.assertRaises(AssertionError):
            assert_no_plaintext("has secret here", ["secret"])

    def test_tempo_service_names(self) -> None:
        payload = {
            "batches": [
                {
                    "resource": {
                        "attributes": [
                            {"key": "service.name", "value": {"stringValue": "incident-api"}},
                        ]
                    },
                    "scopeSpans": [
                        {
                            "spans": [
                                {
                                    "attributes": [
                                        {
                                            "key": "forge.service",
                                            "value": {"stringValue": "incident-api"},
                                        }
                                    ]
                                }
                            ]
                        }
                    ],
                },
                {
                    "resource": {
                        "attributes": [
                            {
                                "key": "service.name",
                                "value": {"stringValue": "incident-classify"},
                            }
                        ]
                    },
                    "scopeSpans": [{"spans": [{}]}],
                },
            ]
        }
        names = tempo_service_names(payload)
        self.assertIn("incident-api", names)
        self.assertIn("incident-classify", names)

    def test_db_status_ok(self) -> None:
        self.assertTrue(
            db_status_ok({"DATABASE_URL_present": True, "backend": "postgres"})
        )
        self.assertFalse(
            db_status_ok(
                {
                    "DATABASE_URL_present": True,
                    "backend": "postgres",
                    "leak": "postgresql://x",
                }
            )
        )

    def test_secret_status_ok(self) -> None:
        body = {
            "APP_SHARED_SECRET_present": True,
            "PRODUCT_MODE_present": True,
            "value_length": 8,
        }
        self.assertTrue(secret_status_ok(body, ["hunter2"]))
        self.assertFalse(secret_status_ok({**body, "x": "hunter2"}, ["hunter2"]))
        self.assertEqual(json.loads(json.dumps(body))["value_length"], 8)


if __name__ == "__main__":
    unittest.main()
