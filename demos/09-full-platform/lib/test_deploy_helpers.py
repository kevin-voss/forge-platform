#!/usr/bin/env python3
"""Unit tests for deploy orchestration helpers."""

from __future__ import annotations

import json
import unittest

from deploy_helpers import (
    deployment_status,
    parse_build_accept,
    parse_build_image,
    parse_json_id,
    product_hostnames,
    route_hosts,
    wait_until,
)


class DeployHelpersTest(unittest.TestCase):
    def test_parse_json_id(self) -> None:
        self.assertEqual(parse_json_id('{"id":"abc-123"}'), "abc-123")
        self.assertEqual(parse_json_id({"id": "x"}, "id"), "x")
        with self.assertRaises(ValueError):
            parse_json_id("{}")

    def test_parse_build_accept(self) -> None:
        bid, status = parse_build_accept(
            {"buildId": "11111111-1111-1111-1111-111111111111", "status": "queued"}
        )
        self.assertEqual(status, "queued")
        self.assertTrue(bid.startswith("1111"))

    def test_parse_build_image(self) -> None:
        self.assertIsNone(parse_build_image({"status": "queued"}))
        self.assertEqual(
            parse_build_image({"image": "localhost:5000/capstone-api:deadbeef-abcd"}),
            "localhost:5000/capstone-api:deadbeef-abcd",
        )

    def test_deployment_status(self) -> None:
        self.assertEqual(deployment_status({"status": "active"}), "active")

    def test_route_hosts(self) -> None:
        hosts = route_hosts(
            [
                {"host": "api.demo.localhost", "upstreams": []},
                {"host": "Admin.demo.localhost"},
            ]
        )
        self.assertEqual(hosts, {"api.demo.localhost", "admin.demo.localhost"})

    def test_product_hostnames(self) -> None:
        mapping = product_hostnames(["api", "logs"])
        self.assertEqual(
            mapping,
            {
                "api": "api.demo.localhost",
                "logs": "logs.demo.localhost",
            },
        )

    def test_wait_until(self) -> None:
        state = {"n": 0}

        def ready() -> bool:
            state["n"] += 1
            return state["n"] >= 2

        wait_until(ready, timeout_s=2.0, interval_s=0.01, label="n>=2")
        with self.assertRaises(TimeoutError):
            wait_until(lambda: False, timeout_s=0.05, interval_s=0.02, label="never")

    def test_json_roundtrip_shape(self) -> None:
        raw = json.dumps({"id": "dep-1", "status": "pending"})
        self.assertEqual(parse_json_id(raw), "dep-1")
        self.assertEqual(deployment_status(raw), "pending")


if __name__ == "__main__":
    unittest.main()
