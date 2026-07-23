"""Helpers for capstone failure-injection + incident-response workflow (19.05)."""

from __future__ import annotations

import json
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping

REQUIRED_WORKFLOW_STEPS = (
    "collect-diagnostics",
    "diagnose",
    "approve-rollback",
    "do-rollback",
    "finalize",
)

REQUIRED_REPORT_FIELDS = (
    "run_id",
    "deployment_id",
    "rolled_back",
    "report_ref",
    "generated_at",
)


def parse_workflow_yaml(text: str) -> dict[str, Any]:
    """Minimal YAML subset parser for the capstone incident-response definition."""
    name: str | None = None
    trigger_event: str | None = None
    steps: list[dict[str, Any]] = []
    current: dict[str, Any] | None = None
    in_trigger = False
    in_steps = False

    for raw in text.splitlines():
        if not raw.strip() or raw.strip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        line = raw.strip()

        if indent == 0 and line.startswith("name:"):
            name = line.split(":", 1)[1].strip()
            in_trigger = False
            in_steps = False
            continue
        if indent == 0 and line == "trigger:":
            in_trigger = True
            in_steps = False
            continue
        if indent == 0 and line == "steps:":
            in_trigger = False
            in_steps = True
            continue

        if in_trigger and indent == 2 and line.startswith("event:"):
            trigger_event = line.split(":", 1)[1].strip()
            continue

        if in_steps and indent == 2 and line.startswith("- id:"):
            current = {"id": line.split(":", 1)[1].strip()}
            steps.append(current)
            continue

        if in_steps and current is not None and indent >= 4 and ":" in line:
            key, _, value = line.partition(":")
            key = key.strip()
            value = value.strip()
            if key in {"type", "action", "agent", "prompt", "on_deny", "compensate", "message"}:
                current[key] = value
            continue

    if not name:
        raise ValueError("workflow name is required")
    if not trigger_event:
        raise ValueError("workflow trigger.event is required")
    if not steps:
        raise ValueError("workflow steps are required")

    return {"name": name, "trigger": {"event": trigger_event}, "steps": steps}


def validate_incident_response(payload: Mapping[str, Any]) -> dict[str, Any]:
    name = str(payload.get("name") or "").strip()
    if name != "incident-response":
        raise ValueError(f"expected name incident-response, got {name!r}")

    trigger = payload.get("trigger") or {}
    if not isinstance(trigger, Mapping):
        raise ValueError("trigger must be a mapping")
    event = str(trigger.get("event") or "").strip()
    if event != "deployment.failed":
        raise ValueError(f"trigger.event must be deployment.failed, got {event!r}")

    steps = payload.get("steps")
    if not isinstance(steps, list) or not steps:
        raise ValueError("steps must be a non-empty list")
    by_id = {str(s.get("id")): s for s in steps if isinstance(s, Mapping)}
    for required in REQUIRED_WORKFLOW_STEPS:
        if required not in by_id:
            raise ValueError(f"missing step {required}")

    diagnose = by_id["diagnose"]
    if str(diagnose.get("type") or "") != "agent":
        raise ValueError("diagnose step must be type=agent")
    if str(diagnose.get("agent") or "") != "deployment-investigator":
        raise ValueError("diagnose agent must be deployment-investigator")

    approve = by_id["approve-rollback"]
    if str(approve.get("type") or "") != "approval":
        raise ValueError("approve-rollback must be type=approval")
    if "Approve rollback" not in str(approve.get("prompt") or ""):
        raise ValueError("approval prompt must mention Approve rollback")

    return {
        "name": name,
        "trigger_event": event,
        "step_ids": sorted(by_id),
    }


def load_and_validate_workflow(path: str | Path) -> dict[str, Any]:
    text = Path(path).read_text(encoding="utf-8")
    parsed = parse_workflow_yaml(text)
    return validate_incident_response(parsed)


def build_deployment_failed_event(
    *,
    deployment_id: str,
    service_id: str = "api",
    reason: str = "readiness_failed:capstone_break",
    image_ref: str | None = "localhost:5000/capstone/api:v2-broken",
) -> dict[str, Any]:
    """Documented deployment.failed payload (contracts/events/deployment.failed)."""
    data: dict[str, Any] = {
        "deployment_id": deployment_id,
        "service_id": service_id,
        "reason": reason,
        "failed_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    if image_ref:
        data["image_ref"] = image_ref
    return {
        "subject": "deployment.failed",
        "source": "capstone-scenario",
        "data": data,
    }


def build_completion_event(
    *,
    deployment_id: str,
    service_id: str = "api",
    image_ref: str | None = "localhost:5000/capstone/api:v1",
) -> dict[str, Any]:
    """Documented deployment.completed payload after approved recovery."""
    data: dict[str, Any] = {
        "deployment_id": deployment_id,
        "service_id": service_id,
        "completed_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    if image_ref:
        data["image_ref"] = image_ref
    return {
        "subject": "deployment.completed",
        "source": "capstone-scenario",
        "data": data,
    }


def assert_report_shape(report: Mapping[str, Any], *, rolled_back: bool, deployment_id: str) -> None:
    for field in REQUIRED_REPORT_FIELDS:
        if field not in report:
            raise AssertionError(f"report missing {field}: {sorted(report)}")
    if bool(report.get("rolled_back")) is not rolled_back:
        raise AssertionError(f"rolled_back={report.get('rolled_back')}, want {rolled_back}")
    if str(report.get("deployment_id")) != deployment_id:
        raise AssertionError(f"deployment_id={report.get('deployment_id')!r}")
    ref = str(report.get("report_ref") or "")
    if not re.match(r"^(inline|storage)://", ref):
        raise AssertionError(f"bad report_ref: {ref!r}")


def assert_run_auditable(body: Mapping[str, Any], *, required_steps: tuple[str, ...] = REQUIRED_WORKFLOW_STEPS) -> None:
    steps = {s.get("id"): s for s in (body.get("steps") or []) if isinstance(s, Mapping)}
    for sid in required_steps:
        if sid not in steps:
            raise AssertionError(f"missing auditable step {sid}; have {sorted(steps)}")


def readiness_failure_from_capstone_break(ready_body: Mapping[str, Any] | None, status_code: int) -> bool:
    """True when product readiness failed because CAPSTONE_BREAK is set."""
    if status_code != 503:
        return False
    if not isinstance(ready_body, Mapping):
        return False
    return (
        str(ready_body.get("status") or "") == "not_ready"
        and str(ready_body.get("error") or "") == "capstone_break"
    )


def dumps(obj: Any) -> str:
    return json.dumps(obj, indent=2, sort_keys=True)
