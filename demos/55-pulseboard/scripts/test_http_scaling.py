#!/usr/bin/env python3
"""Unit tests for PulseBoard httpRequests autoscaling math (epic 55.02).

Mirrors forge-autoscaler evaluate math:
  desired = clamp(ceil(totalRPS / targetRequestsPerSecond), minReplicas, maxReplicas)
"""

from __future__ import annotations

import math
import unittest


MIN_REPLICAS = 1
MAX_REPLICAS = 10
TARGET_RPS = 50.0


def recommend_http_rps(total_rps: float) -> int:
    if TARGET_RPS <= 0:
        raise ValueError("targetRequestsPerSecond must be > 0")
    if total_rps <= 0:
        return MIN_REPLICAS
    raw = int(math.ceil(total_rps / TARGET_RPS - 1e-9))
    return max(MIN_REPLICAS, min(MAX_REPLICAS, raw))


class HttpScalingTests(unittest.TestCase):
    def test_idle_stays_at_min(self) -> None:
        self.assertEqual(recommend_http_rps(0), MIN_REPLICAS)
        self.assertEqual(recommend_http_rps(20), MIN_REPLICAS)

    def test_load_scales_within_bounds(self) -> None:
        # 100 / 50 → 2
        self.assertEqual(recommend_http_rps(100), 2)
        # 250 / 50 → 5
        self.assertEqual(recommend_http_rps(250), 5)
        # 450 / 50 → 9
        self.assertEqual(recommend_http_rps(450), 9)
        # 600 / 50 → 12 → clamped to max 10
        self.assertEqual(recommend_http_rps(600), MAX_REPLICAS)

    def test_replicas_never_outside_bounds(self) -> None:
        for rps in (0, 1, 49, 50, 51, 250, 1000, 1e6):
            desired = recommend_http_rps(rps)
            self.assertGreaterEqual(desired, MIN_REPLICAS)
            self.assertLessEqual(desired, MAX_REPLICAS)

    def test_policy_fixture_shape(self) -> None:
        import pathlib

        path = pathlib.Path(__file__).resolve().parents[1] / "fixtures" / "scaling-policy.yaml"
        if not path.exists():
            self.skipTest("fixture missing")
        text = path.read_text()
        self.assertIn("kind: ScalingPolicy", text)
        self.assertIn("kind: Application", text)
        self.assertIn("name: pulseboard-api", text)
        self.assertIn("type: httpRequests", text)
        self.assertIn("targetValue: 50", text)
        self.assertIn("targetRequestsPerSecond: 50", text)
        self.assertIn("minReplicas: 1", text)
        self.assertIn("maxReplicas: 10", text)


if __name__ == "__main__":
    unittest.main()
