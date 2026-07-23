"""Unit tests for capstone 19.05 scenario helpers."""

from __future__ import annotations

import unittest
from pathlib import Path

from scenario_helpers import (
    assert_report_shape,
    build_completion_event,
    build_deployment_failed_event,
    load_and_validate_workflow,
    parse_workflow_yaml,
    readiness_failure_from_capstone_break,
    validate_incident_response,
)

DEMO_DIR = Path(__file__).resolve().parents[1]
WORKFLOW_PATH = DEMO_DIR / "scenario" / "incident-response.yaml"


class TestWorkflowDefinition(unittest.TestCase):
    def test_incident_response_validates(self) -> None:
        validated = load_and_validate_workflow(WORKFLOW_PATH)
        self.assertEqual(validated["name"], "incident-response")
        self.assertEqual(validated["trigger_event"], "deployment.failed")
        self.assertIn("diagnose", validated["step_ids"])
        self.assertIn("approve-rollback", validated["step_ids"])

    def test_parse_and_validate_inline(self) -> None:
        text = WORKFLOW_PATH.read_text(encoding="utf-8")
        parsed = parse_workflow_yaml(text)
        out = validate_incident_response(parsed)
        self.assertEqual(out["name"], "incident-response")


class TestEventsAndReport(unittest.TestCase):
    def test_deployment_failed_event_shape(self) -> None:
        ev = build_deployment_failed_event(deployment_id="dep-broken")
        self.assertEqual(ev["subject"], "deployment.failed")
        data = ev["data"]
        self.assertEqual(data["deployment_id"], "dep-broken")
        self.assertEqual(data["reason"], "readiness_failed:capstone_break")
        self.assertIn("failed_at", data)

    def test_completion_event_shape(self) -> None:
        ev = build_completion_event(deployment_id="dep-broken")
        self.assertEqual(ev["subject"], "deployment.completed")
        self.assertEqual(ev["data"]["deployment_id"], "dep-broken")
        self.assertIn("completed_at", ev["data"])

    def test_report_shape(self) -> None:
        report = {
            "run_id": "run-1",
            "deployment_id": "dep-1",
            "rolled_back": True,
            "report_ref": "storage://wf-reports/workflow-reports/run-1.json",
            "generated_at": "2026-07-23T12:00:00Z",
        }
        assert_report_shape(report, rolled_back=True, deployment_id="dep-1")

    def test_capstone_break_readiness_detection(self) -> None:
        self.assertTrue(
            readiness_failure_from_capstone_break(
                {"status": "not_ready", "error": "capstone_break"},
                503,
            )
        )
        self.assertFalse(readiness_failure_from_capstone_break({"status": "ok"}, 200))


if __name__ == "__main__":
    unittest.main()
