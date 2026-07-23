"""Unit + integration tests for seed agents (15.07)."""

from __future__ import annotations

import time
from pathlib import Path

import yaml
from fastapi.testclient import TestClient

from app.agents.loader import DEFAULT_AGENTS_DIR, load_registry
from app.agents.models import AgentDefinition
from app.tools.registry import build_tool_registry

SEED_NAMES = {
    "deployment-investigator",
    "log-summarizer",
    "docs-assistant",
    "release-reviewer",
    "infra-health",
}

PROJECT = {"X-Forge-Project": "proj-a"}
ACTOR = {"X-Forge-Actor": "alice"}


def test_each_seed_yaml_loads_and_validates() -> None:
    assert DEFAULT_AGENTS_DIR.is_dir()
    for name in sorted(SEED_NAMES):
        path = DEFAULT_AGENTS_DIR / f"{name}.yaml"
        assert path.is_file(), f"missing seed agent {path}"
        payload = yaml.safe_load(path.read_text(encoding="utf-8"))
        agent = AgentDefinition.model_validate(payload)
        assert agent.name == name
        assert agent.model == "local-general"
        assert agent.limits.max_steps >= 1
        assert agent.tools


def test_packaged_registry_includes_all_seeds() -> None:
    registry = load_registry(None)
    names = {a.name for a in registry.list()}
    assert SEED_NAMES.issubset(names)
    assert "fixture-echo" in names


def test_seed_agents_least_privilege_and_single_destructive() -> None:
    registry = load_registry(None)
    tools = {t.name: t for t in build_tool_registry("fake").list()}
    destructive_agents: list[str] = []
    for name in SEED_NAMES:
        agent = registry.get(name)
        assert agent is not None
        for tool_name in agent.tools:
            tool = tools.get(tool_name)
            assert tool is not None, f"{name} declares unknown tool {tool_name}"
            for perm in tool.required_permissions:
                assert perm in agent.permissions, (
                    f"{name} missing permission {perm} required by {tool_name}"
                )
            if tool.destructive:
                destructive_agents.append(name)
    assert destructive_agents == ["deployment-investigator"]
    investigator = registry.get("deployment-investigator")
    assert investigator is not None
    assert "runtime.restart" in investigator.tools


def test_api_lists_seed_agents(client: TestClient) -> None:
    resp = client.get("/v1/agents")
    assert resp.status_code == 200
    names = {a["name"] for a in resp.json()["agents"]}
    assert SEED_NAMES.issubset(names)


def test_log_summarizer_run_completes_dry_run(client: TestClient) -> None:
    start = client.post(
        "/v1/agents/log-summarizer/runs",
        headers=PROJECT,
        json={"input": "errors x3", "context": {"dry_run": True}},
    )
    assert start.status_code == 202, start.text
    run_id = start.json()["run_id"]
    body = _wait_status(client, run_id, {"succeeded", "failed", "stopped", "cancelled"})
    assert body["status"] == "succeeded"
    assert body.get("steps")


def test_deployment_investigator_awaiting_approval_then_deny(client: TestClient) -> None:
    start = client.post(
        "/v1/agents/deployment-investigator/runs",
        headers=PROJECT,
        json={
            "input": "restart dep-1",
            "context": {
                "dry_run": True,
                "tool": "runtime.restart",
                "tool_args": {"deployment_id": "dep-1"},
            },
        },
    )
    assert start.status_code == 202, start.text
    run_id = start.json()["run_id"]
    body = _wait_status(client, run_id, {"awaiting_approval"})
    assert body["status"] == "awaiting_approval"
    pending = body.get("pending_approval")
    assert pending is not None
    assert pending["tool"] == "runtime.restart"
    approval_id = pending["id"]

    denied = client.post(
        f"/v1/approvals/{approval_id}/deny",
        headers={**PROJECT, **ACTOR},
        json={"reason": "manual"},
    )
    assert denied.status_code == 200
    assert denied.json()["status"] == "denied"

    final = _wait_status(client, run_id, {"succeeded", "failed", "stopped", "cancelled"})
    assert final["status"] in {"succeeded", "stopped", "failed"}
    # Tool must not have executed successfully after deny.
    tool_steps = [s for s in final.get("steps", []) if s.get("type") == "tool"]
    for step in tool_steps:
        if step.get("tool") == "runtime.restart":
            obs = step.get("observation") or {}
            assert obs.get("ok") is False or obs.get("approval_status") == "denied"


def _wait_status(
    client: TestClient,
    run_id: str,
    statuses: set[str],
    *,
    timeout: float = 5.0,
) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        resp = client.get(f"/v1/runs/{run_id}", headers=PROJECT)
        assert resp.status_code == 200, resp.text
        body = resp.json()
        if body["status"] in statuses:
            return body
        time.sleep(0.02)
    raise AssertionError(f"run {run_id} did not reach {statuses}")


def test_seed_docs_exist() -> None:
    # Repo-root docs/ are available in the workspace checkout, but not inside the
    # service Docker build context (/src). Skip when the monorepo root is absent.
    service_root = Path(__file__).resolve().parents[1]
    repo_root = service_root.parent.parent
    docs = repo_root / "docs" / "agents" / "seed-agents.md"
    if not docs.is_file():
        import pytest

        pytest.skip("docs/agents/seed-agents.md not present in this checkout/context")
    text = docs.read_text(encoding="utf-8")
    for name in SEED_NAMES:
        assert name in text
