"""Unit tests for AI diagnosis helpers + fixtures (19.04)."""

from __future__ import annotations

import json
import unittest
from pathlib import Path

from ai_helpers import (
    REQUIRED_INVESTIGATOR_TOOLS,
    build_investigator_plan,
    collection_create_body,
    diagnosis_cites_telemetry_and_memory,
    load_json,
    nn_top_id,
    parse_simple_yaml_mapping,
    upsert_body,
    validate_agent_config,
    validate_historical_incidents,
)

DEMO_DIR = Path(__file__).resolve().parents[1]
AI_DIR = DEMO_DIR / "ai"
FIXTURES_DIR = AI_DIR / "fixtures"


class AiHelpersTest(unittest.TestCase):
    def test_historical_incidents_fixture(self) -> None:
        fx = load_json(FIXTURES_DIR / "historical-incidents.json")
        validated = validate_historical_incidents(fx)
        self.assertEqual(validated["collection"], "incidents")
        self.assertEqual(validated["expected_id"], "incident-readiness-broken-release")
        self.assertGreaterEqual(validated["count"], 3)
        body = collection_create_body(fx)
        self.assertEqual(body["name"], "incidents")
        self.assertEqual(body["dim"], 384)
        upsert = upsert_body(fx)
        self.assertEqual(len(upsert["items"]), validated["count"])
        self.assertEqual(upsert["model"], "local-embed-small")

    def test_agent_config_validates(self) -> None:
        text = (AI_DIR / "deployment-investigator.yaml").read_text(encoding="utf-8")
        payload = parse_simple_yaml_mapping(text)
        agent = validate_agent_config(payload)
        self.assertEqual(agent["name"], "deployment-investigator")
        for tool in REQUIRED_INVESTIGATOR_TOOLS:
            self.assertIn(tool, agent["tools"])
        self.assertIn("memory:read", agent["permissions"])
        self.assertIn("runtime.restart", agent["tools"])

    def test_memory_search_fixture_matches_expected(self) -> None:
        hist = load_json(FIXTURES_DIR / "historical-incidents.json")
        mem = load_json(FIXTURES_DIR / "memory.search.json")
        expected = hist["expected_id"]
        self.assertEqual(mem["results"][0]["id"], expected)

    def test_nn_top_id(self) -> None:
        self.assertEqual(
            nn_top_id({"results": [{"id": "a", "score": 0.9}, {"id": "b", "score": 0.1}]}),
            "a",
        )

    def test_build_plan_includes_classification_and_restart(self) -> None:
        plan = build_investigator_plan(
            deployment_id="dep-capstone",
            collection="incidents",
            query="readiness probe failed",
            expected_memory_id="incident-readiness-broken-release",
            classification={"label": "infra.readiness_failure", "source": "incident-classify"},
        )
        tools = [p["tool"] for p in plan if p["kind"] == "tool_call"]
        self.assertEqual(
            tools,
            [
                "deployment.read",
                "logs.search",
                "metrics.query",
                "memory.search",
                "runtime.restart",
            ],
        )
        mem = next(p for p in plan if p.get("tool") == "memory.search")
        self.assertIn("infra.readiness_failure", mem["args"]["query"])

    def test_diagnosis_assertions(self) -> None:
        run = {
            "status": "awaiting_approval",
            "pending_approval": {
                "tool": "runtime.restart",
                "status": "pending",
                "args": {"deployment_id": "dep-capstone"},
            },
            "steps": [
                {
                    "type": "tool",
                    "tool": "deployment.read",
                    "observation": {
                        "ok": True,
                        "deployment_id": "dep-capstone",
                        "ready": False,
                        "status": "degraded",
                        "trace_id": "capstone-trace-readiness-001",
                    },
                },
                {
                    "type": "tool",
                    "tool": "logs.search",
                    "observation": {
                        "ok": True,
                        "entries": [
                            {
                                "message": "readiness probe failed: connection refused",
                                "trace_id": "capstone-trace-readiness-001",
                            }
                        ],
                    },
                },
                {
                    "type": "tool",
                    "tool": "metrics.query",
                    "observation": {
                        "ok": True,
                        "samples": [{"value": 0}],
                    },
                },
                {
                    "type": "tool",
                    "tool": "memory.search",
                    "args": {
                        "collection": "incidents",
                        "query": "broken release classification=infra.readiness_failure",
                    },
                    "observation": {
                        "ok": True,
                        "results": [
                            {"id": "incident-readiness-broken-release", "score": 0.94}
                        ],
                    },
                },
            ],
        }
        text = diagnosis_cites_telemetry_and_memory(
            run,
            expected_memory_id="incident-readiness-broken-release",
            deployment_id="dep-capstone",
            classification_label="infra.readiness_failure",
        )
        self.assertIn("incident-readiness-broken-release", text)
        self.assertIn("approval-gated", text)

    def test_diagnosis_rejects_executed_restart(self) -> None:
        run = {
            "status": "awaiting_approval",
            "pending_approval": {"tool": "runtime.restart", "status": "pending"},
            "steps": [
                {
                    "type": "tool",
                    "tool": "deployment.read",
                    "observation": {
                        "ok": True,
                        "deployment_id": "dep-x",
                        "ready": False,
                    },
                },
                {
                    "type": "tool",
                    "tool": "logs.search",
                    "observation": {
                        "ok": True,
                        "entries": [{"message": "readiness probe failed"}],
                    },
                },
                {
                    "type": "tool",
                    "tool": "metrics.query",
                    "observation": {"ok": True, "samples": [{"value": 0}]},
                },
                {
                    "type": "tool",
                    "tool": "memory.search",
                    "args": {"query": "x"},
                    "observation": {
                        "ok": True,
                        "results": [{"id": "m1", "score": 1.0}],
                    },
                },
                {
                    "type": "tool",
                    "tool": "runtime.restart",
                    "observation": {"ok": True},
                },
            ],
        }
        with self.assertRaises(AssertionError):
            diagnosis_cites_telemetry_and_memory(
                run, expected_memory_id="m1", deployment_id="dep-x"
            )


if __name__ == "__main__":
    unittest.main()
