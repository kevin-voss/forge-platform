#!/usr/bin/env python3
"""Unit tests for SnapNote worker queueDepth autoscaling math (epic 52.04).

Mirrors forge-autoscaler evaluate math:
  desired = clamp(ceil(depth / targetPerReplica), minReplicas, maxReplicas)
  retryRate above target blocks scale-down.
"""

from __future__ import annotations

import math
import unittest


MIN_REPLICAS = 1
MAX_REPLICAS = 8
TARGET_PER_REPLICA = 20.0
RETRY_TARGET = 0.05


def recommend_queue_depth(depth: float, current: int = MIN_REPLICAS) -> int:
    if TARGET_PER_REPLICA <= 0:
        raise ValueError("targetPerReplica must be > 0")
    raw = math.ceil(depth / TARGET_PER_REPLICA) if depth > 0 else MIN_REPLICAS
    if depth <= 0:
        raw = MIN_REPLICAS
    return max(MIN_REPLICAS, min(MAX_REPLICAS, int(raw)))


def apply_retry_guard(
    depth_desired: int,
    current: int,
    retry_rate: float,
) -> tuple[int, bool]:
    """Return (desired, blocked_scale_down)."""
    if retry_rate > RETRY_TARGET and depth_desired < current:
        return current, True
    return depth_desired, False


class QueueScalingTests(unittest.TestCase):
    def test_empty_queue_stays_at_min(self) -> None:
        self.assertEqual(recommend_queue_depth(0), MIN_REPLICAS)

    def test_burst_scales_within_bounds(self) -> None:
        # 40 / 20 → 2
        self.assertEqual(recommend_queue_depth(40), 2)
        # 80 / 20 → 4
        self.assertEqual(recommend_queue_depth(80), 4)
        # 200 / 20 → 10 → clamped to max 8
        self.assertEqual(recommend_queue_depth(200), MAX_REPLICAS)

    def test_replicas_never_outside_bounds(self) -> None:
        for depth in (0, 1, 19, 20, 21, 100, 1000, 1e6):
            desired = recommend_queue_depth(depth)
            self.assertGreaterEqual(desired, MIN_REPLICAS)
            self.assertLessEqual(desired, MAX_REPLICAS)

    def test_retry_pressure_blocks_scale_down(self) -> None:
        # Empty backlog would want min=1, but retry holds at current=4.
        desired, blocked = apply_retry_guard(
            depth_desired=recommend_queue_depth(0),
            current=4,
            retry_rate=0.06,
        )
        self.assertTrue(blocked)
        self.assertEqual(desired, 4)

    def test_healthy_retry_allows_scale_down(self) -> None:
        desired, blocked = apply_retry_guard(
            depth_desired=recommend_queue_depth(0),
            current=4,
            retry_rate=0.01,
        )
        self.assertFalse(blocked)
        self.assertEqual(desired, MIN_REPLICAS)

    def test_policy_fixture_shape(self) -> None:
        import pathlib

        path = pathlib.Path(__file__).resolve().parents[1] / "fixtures" / "scaling-policy.yaml"
        if not path.exists():
            self.skipTest("fixture missing")
        text = path.read_text()
        self.assertIn("kind: ScalingPolicy", text)
        self.assertIn("kind: Worker", text)
        self.assertIn("name: snapnote-worker", text)
        self.assertIn("type: queueDepth", text)
        self.assertIn("queue: snapnote-attachments", text)
        self.assertIn("targetValue: 20", text)
        self.assertIn("retryRate", text)  # documented; armed transiently in run.sh
        self.assertIn("queue: snapnote-attachments", text)
        self.assertIn("minReplicas: 1", text)
        self.assertIn("maxReplicas: 8", text)


if __name__ == "__main__":
    unittest.main()
