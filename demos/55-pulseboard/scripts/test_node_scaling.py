#!/usr/bin/env python3
"""Unit tests for PulseBoard node autoscaling bounds (epic 55.03).

Node count must stay within [minNodes, maxNodes]. Scale-up is driven by
unschedulable demand (pending placements / reservation); scale-down drains
idle nodes back to minNodes.
"""

from __future__ import annotations

import math
import pathlib
import unittest


MIN_NODES = 2
MAX_NODES = 3
SLOTS_PER_NODE = 2  # docker-small


def clamp_nodes(desired: int) -> int:
    return max(MIN_NODES, min(MAX_NODES, desired))


def nodes_for_slots(slots_needed: int) -> int:
    if slots_needed <= 0:
        return MIN_NODES
    raw = int(math.ceil(slots_needed / SLOTS_PER_NODE - 1e-9))
    return clamp_nodes(raw)


class NodeScalingTests(unittest.TestCase):
    def test_idle_stays_at_min(self) -> None:
        # Baseline: api(1) + web(1) = 2 slots → still minNodes=2 (pool floor).
        self.assertEqual(nodes_for_slots(2), MIN_NODES)
        self.assertEqual(clamp_nodes(0), MIN_NODES)
        self.assertEqual(clamp_nodes(1), MIN_NODES)

    def test_unschedulable_demand_scales_within_bounds(self) -> None:
        # 5 api + 1 web = 6 slots → 3 nodes (hits max).
        self.assertEqual(nodes_for_slots(6), MAX_NODES)
        # 3 api + 1 web = 4 slots → 2 nodes (fits min pool).
        self.assertEqual(nodes_for_slots(4), MIN_NODES)
        # Overshoot clamps to maxNodes.
        self.assertEqual(nodes_for_slots(100), MAX_NODES)

    def test_nodes_never_outside_bounds(self) -> None:
        for slots in (0, 1, 2, 3, 4, 5, 6, 7, 20, 1000):
            n = nodes_for_slots(slots)
            self.assertGreaterEqual(n, MIN_NODES)
            self.assertLessEqual(n, MAX_NODES)

    def test_scale_down_returns_to_min(self) -> None:
        # After load stops, only baseline slots remain → minNodes.
        self.assertEqual(nodes_for_slots(2), MIN_NODES)

    def test_nodepool_fixture_shape(self) -> None:
        path = pathlib.Path(__file__).resolve().parents[1] / "fixtures" / "nodepool-docker.yaml"
        if not path.exists():
            self.skipTest("fixture missing")
        text = path.read_text()
        self.assertIn("kind: InfrastructureProvider", text)
        self.assertIn("kind: NodePool", text)
        self.assertIn("name: pulseboard-pool", text)
        self.assertIn("providerRef: docker-local", text)
        self.assertIn("type: docker", text)
        self.assertIn("machineType: docker-small", text)
        self.assertIn("minNodes: 2", text)
        self.assertIn("maxNodes: 3", text)


if __name__ == "__main__":
    unittest.main()
