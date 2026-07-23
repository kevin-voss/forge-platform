"""Unit + integration tests for human approval gate (15.06)."""

from __future__ import annotations

import asyncio
import time
from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

from app.agents.loader import load_registry
from app.approvals.store import APPROVED, DENIED, EXPIRED, PENDING, ApprovalStore
from app.config import clear_settings_cache
from app.main import create_app
from app.permissions import PermissionChecker
from app.run.engine import RunEngine, StartRunRequest
from app.run.model_client import FakeModelClient
from app.run.store import RunStore
from app.tools.invoker import ToolInvoker
from app.tools.registry import build_tool_registry

PROJECT = {"X-Forge-Project": "proj-a"}
ACTOR = {"X-Forge-Actor": "alice"}


def _write_restart_agent(directory: Path, *, max_steps: int = 4) -> None:
    payload = {
        "name": "restart-agent",
        "model": "local-general",
        "tools": ["runtime.restart", "echo.ping"],
        "permissions": ["runtime:restart", "project:read"],
        "limits": {"max_steps": max_steps, "timeout_seconds": 30},
    }
    (directory / "restart-agent.yaml").write_text(yaml.safe_dump(payload), encoding="utf-8")


def _engine(tmp_path: Path, *, approval_ttl_seconds: int = 3600) -> RunEngine:
    _write_restart_agent(tmp_path)
    registry = load_registry(str(tmp_path))
    store = RunStore(tmp_path / "runs.db")
    approvals = ApprovalStore(tmp_path / "runs.db", conn=store.connection)
    invoker = ToolInvoker(build_tool_registry("fake"), checker=PermissionChecker())
    return RunEngine(
        store=store,
        registry=registry,
        invoker=invoker,
        model_client=FakeModelClient(),
        fake_model_client=FakeModelClient(),
        approvals=approvals,
        approval_ttl_seconds=approval_ttl_seconds,
        max_concurrent_runs=4,
    )


async def _wait_status(
    engine: RunEngine,
    run_id: str,
    status: str,
    *,
    timeout: float = 5.0,
) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        run = engine.store.get_run(run_id)
        assert run is not None
        if run.status == status:
            return run.to_api_dict()
        await asyncio.sleep(0.02)
    raise AssertionError(f"run {run_id} did not reach {status}")


async def _wait_terminal(engine: RunEngine, run_id: str, *, timeout: float = 5.0) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        run = engine.store.get_run(run_id)
        assert run is not None
        if run.status not in {"running", "awaiting_approval"}:
            return run.to_api_dict()
        await asyncio.sleep(0.02)
    raise AssertionError(f"run {run_id} did not terminate")


def test_destructive_tool_creates_approval_and_pauses(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="restart-agent",
                    project_id="proj-a",
                    run_input="restart dep-1",
                    context={
                        "dry_run": True,
                        "tool": "runtime.restart",
                        "tool_args": {"deployment_id": "dep-1"},
                    },
                )
            )
            body = await _wait_status(engine, run.id, "awaiting_approval")
            assert body["status"] == "awaiting_approval"
            assert engine.approvals is not None
            pending = engine.approvals.get_pending_for_run(run.id)
            assert pending is not None
            assert pending.tool == "runtime.restart"
            assert pending.args == {"deployment_id": "dep-1"}
            assert pending.status == PENDING
            tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
            assert tool_steps == []
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_approve_resumes_and_executes(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="restart-agent",
                    project_id="proj-a",
                    run_input="restart dep-1",
                    context={
                        "dry_run": True,
                        "tool": "runtime.restart",
                        "tool_args": {"deployment_id": "dep-1"},
                    },
                )
            )
            await _wait_status(engine, run.id, "awaiting_approval")
            assert engine.approvals is not None
            pending = engine.approvals.get_pending_for_run(run.id)
            assert pending is not None
            outcome = engine.approvals.decide(
                pending.id,
                status=APPROVED,
                decided_by="alice",
            )
            assert outcome == "ok"
            await engine.notify_approval_decision(pending.id)
            body = await _wait_terminal(engine, run.id)
            assert body["status"] == "succeeded"
            tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
            assert tool_steps
            assert tool_steps[0]["tool"] == "runtime.restart"
            assert tool_steps[0]["observation"]["ok"] is True
            assert tool_steps[0]["observation"]["restarted"] is True
            assert tool_steps[0]["observation"]["approval_status"] == APPROVED
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_deny_skips_tool_execution(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="restart-agent",
                    project_id="proj-a",
                    run_input="restart dep-1",
                    context={
                        "dry_run": True,
                        "tool": "runtime.restart",
                        "tool_args": {"deployment_id": "dep-1"},
                    },
                )
            )
            await _wait_status(engine, run.id, "awaiting_approval")
            assert engine.approvals is not None
            pending = engine.approvals.get_pending_for_run(run.id)
            assert pending is not None
            engine.approvals.decide(
                pending.id,
                status=DENIED,
                decided_by="bob",
                reason="not safe",
            )
            await engine.notify_approval_decision(pending.id)
            body = await _wait_terminal(engine, run.id)
            assert body["status"] in {"succeeded", "stopped"}
            tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
            assert tool_steps
            assert tool_steps[0]["observation"]["ok"] is False
            assert tool_steps[0]["observation"]["reason"] == "approval_denied"
            assert "restarted" not in tool_steps[0]["observation"]
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_expiry_treated_as_deny(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path, approval_ttl_seconds=1)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="restart-agent",
                    project_id="proj-a",
                    run_input="restart dep-1",
                    context={
                        "dry_run": True,
                        "tool": "runtime.restart",
                        "tool_args": {"deployment_id": "dep-1"},
                    },
                )
            )
            await _wait_status(engine, run.id, "awaiting_approval")
            assert engine.approvals is not None
            pending = engine.approvals.get_pending_for_run(run.id)
            assert pending is not None
            body = await _wait_terminal(engine, run.id, timeout=8.0)
            decided = engine.approvals.get(pending.id)
            assert decided is not None
            assert decided.status == EXPIRED
            tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
            assert tool_steps
            assert tool_steps[0]["observation"]["reason"] == "approval_expired"
            assert "restarted" not in tool_steps[0]["observation"]
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_api_approve_deny_and_cross_project(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    monkeypatch.setenv("PORT", "4301")
    monkeypatch.setenv("FORGE_LOG_LEVEL", "error")
    monkeypatch.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    monkeypatch.setenv("FORGE_AGENTS_DB_PATH", str(tmp_path / "api.db"))
    monkeypatch.setenv("FORGE_AGENTS_DEFS_DIR", str(tmp_path))
    _write_restart_agent(tmp_path)
    clear_settings_cache()

    application = create_app()
    with TestClient(application) as client:
        started = client.post(
            "/v1/agents/restart-agent/runs",
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
        assert started.status_code == 202
        run_id = started.json()["run_id"]

        pending = None
        for _ in range(50):
            detail = client.get(f"/v1/runs/{run_id}", headers=PROJECT)
            assert detail.status_code == 200
            if detail.json()["status"] == "awaiting_approval":
                pending = detail.json().get("pending_approval")
                break
            time.sleep(0.05)
        assert pending is not None
        approval_id = pending["id"]

        listed = client.get("/v1/approvals", headers=PROJECT)
        assert listed.status_code == 200
        assert any(a["id"] == approval_id for a in listed.json()["approvals"])

        cross = client.get(
            f"/v1/approvals/{approval_id}",
            headers={"X-Forge-Project": "other"},
        )
        assert cross.status_code == 404

        denied = client.post(
            f"/v1/approvals/{approval_id}/deny",
            headers={**PROJECT, **ACTOR},
            json={"reason": "manual"},
        )
        assert denied.status_code == 200
        assert denied.json() == {"status": "denied"}

        again = client.post(
            f"/v1/approvals/{approval_id}/approve",
            headers={**PROJECT, **ACTOR},
        )
        assert again.status_code == 409

        body: dict = {}
        for _ in range(50):
            body = client.get(f"/v1/runs/{run_id}", headers=PROJECT).json()
            if body["status"] not in {"running", "awaiting_approval"}:
                break
            time.sleep(0.05)
        tool_steps = [s for s in body.get("steps", []) if s["type"] == "tool"]
        assert tool_steps
        assert tool_steps[0]["observation"]["ok"] is False

    clear_settings_cache()


def test_restart_while_awaiting_still_resumable(tmp_path: Path) -> None:
    """Persist awaiting state, reopen store/engine, recover, then approve."""
    db = tmp_path / "recover.db"
    _write_restart_agent(tmp_path)

    async def _pause() -> str:
        registry = load_registry(str(tmp_path))
        store = RunStore(db)
        approvals = ApprovalStore(db, conn=store.connection)
        invoker = ToolInvoker(build_tool_registry("fake"), checker=PermissionChecker())
        engine = RunEngine(
            store=store,
            registry=registry,
            invoker=invoker,
            model_client=FakeModelClient(),
            fake_model_client=FakeModelClient(),
            approvals=approvals,
            approval_ttl_seconds=3600,
        )
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="restart-agent",
                    project_id="proj-a",
                    run_input="restart dep-1",
                    context={
                        "dry_run": True,
                        "tool": "runtime.restart",
                        "tool_args": {"deployment_id": "dep-1"},
                    },
                )
            )
            await _wait_status(engine, run.id, "awaiting_approval")
            await engine.aclose()
            return run.id
        finally:
            store.close()

    run_id = asyncio.run(_pause())

    async def _recover_and_approve() -> dict:
        registry = load_registry(str(tmp_path))
        store = RunStore(db)
        approvals = ApprovalStore(db, conn=store.connection)
        invoker = ToolInvoker(build_tool_registry("fake"), checker=PermissionChecker())
        engine = RunEngine(
            store=store,
            registry=registry,
            invoker=invoker,
            model_client=FakeModelClient(),
            fake_model_client=FakeModelClient(),
            approvals=approvals,
            approval_ttl_seconds=3600,
        )
        try:
            run = store.get_run(run_id)
            assert run is not None
            assert run.status == "awaiting_approval"
            recovered = await engine.recover_awaiting_runs()
            assert recovered == 1
            pending = approvals.get_pending_for_run(run_id)
            assert pending is not None
            approvals.decide(pending.id, status=APPROVED, decided_by="alice")
            await engine.notify_approval_decision(pending.id)
            return await _wait_terminal(engine, run_id)
        finally:
            await engine.aclose()
            store.close()

    body = asyncio.run(_recover_and_approve())
    assert body["status"] == "succeeded"
    tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
    assert tool_steps[0]["observation"]["restarted"] is True


def test_api_approve_executes_restart(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    monkeypatch.setenv("PORT", "4301")
    monkeypatch.setenv("FORGE_LOG_LEVEL", "error")
    monkeypatch.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    monkeypatch.setenv("FORGE_AGENTS_DB_PATH", str(tmp_path / "approve.db"))
    monkeypatch.setenv("FORGE_AGENTS_DEFS_DIR", str(tmp_path))
    _write_restart_agent(tmp_path)
    clear_settings_cache()

    application = create_app()
    with TestClient(application) as client:
        started = client.post(
            "/v1/agents/restart-agent/runs",
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
        assert started.status_code == 202
        run_id = started.json()["run_id"]

        approval_id = None
        for _ in range(50):
            detail = client.get(f"/v1/runs/{run_id}", headers=PROJECT).json()
            if detail["status"] == "awaiting_approval":
                approval_id = detail["pending_approval"]["id"]
                break
            time.sleep(0.05)
        assert approval_id is not None

        approved = client.post(
            f"/v1/approvals/{approval_id}/approve",
            headers={**PROJECT, **ACTOR},
        )
        assert approved.status_code == 200
        assert approved.json() == {"status": "approved"}

        body: dict = {}
        for _ in range(50):
            body = client.get(f"/v1/runs/{run_id}", headers=PROJECT).json()
            if body["status"] not in {"running", "awaiting_approval"}:
                break
            time.sleep(0.05)
        assert body["status"] == "succeeded"
        tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
        assert tool_steps[0]["observation"]["restarted"] is True

    clear_settings_cache()
