"""Unit tests for RunEngine limits, history, and cancel."""

from __future__ import annotations

import asyncio
import time
from pathlib import Path

import yaml

from app.agents.loader import load_registry
from app.permissions import PermissionChecker
from app.run.engine import RunEngine, StartRunRequest
from app.run.model_client import FakeModelClient
from app.run.store import RunStore
from app.tools.invoker import ToolInvoker
from app.tools.registry import build_tool_registry


def _write_agent(directory: Path, *, max_steps: int = 3, timeout_seconds: int = 30) -> None:
    payload = {
        "name": "fixture-echo",
        "model": "local-general",
        "tools": ["echo.ping"],
        "permissions": ["project:read"],
        "limits": {"max_steps": max_steps, "timeout_seconds": timeout_seconds},
    }
    (directory / "fixture-echo.yaml").write_text(yaml.safe_dump(payload), encoding="utf-8")


def _engine(tmp_path: Path, *, max_steps: int = 3, timeout_seconds: int = 30) -> RunEngine:
    _write_agent(tmp_path, max_steps=max_steps, timeout_seconds=timeout_seconds)
    registry = load_registry(str(tmp_path))
    store = RunStore(tmp_path / "runs.db")
    invoker = ToolInvoker(build_tool_registry("fake"), checker=PermissionChecker())
    return RunEngine(
        store=store,
        registry=registry,
        invoker=invoker,
        model_client=FakeModelClient(),
        fake_model_client=FakeModelClient(),
        max_concurrent_runs=4,
    )


async def _wait_terminal(engine: RunEngine, run_id: str, *, timeout: float = 5.0) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        run = engine.store.get_run(run_id)
        assert run is not None
        if run.status != "running":
            return run.to_api_dict()
        await asyncio.sleep(0.02)
    raise AssertionError(f"run {run_id} did not terminate")


def test_loop_stops_at_max_steps(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path, max_steps=2, timeout_seconds=30)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="fixture-echo",
                    project_id="proj-a",
                    run_input="loop",
                    context={"dry_run": True, "force_loop": True},
                )
            )
            body = await _wait_terminal(engine, run.id)
            assert body["status"] == "stopped"
            assert body["error"] == "max_steps_exceeded"
            model_steps = [s for s in body["steps"] if s["type"] == "model"]
            assert len(model_steps) == 2
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_timeout_aborts_long_run(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path, max_steps=10, timeout_seconds=1)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="fixture-echo",
                    project_id="proj-a",
                    run_input="slow",
                    context={"dry_run": True, "decide_delay_seconds": 3},
                )
            )
            body = await _wait_terminal(engine, run.id, timeout=5.0)
            assert body["status"] == "failed"
            assert body["error"] == "timeout"
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_history_records_steps_in_order(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path, max_steps=3, timeout_seconds=30)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="fixture-echo",
                    project_id="proj-a",
                    run_input="hello",
                    context={"dry_run": True},
                )
            )
            body = await _wait_terminal(engine, run.id)
            assert body["status"] == "succeeded"
            types = [s["type"] for s in body["steps"]]
            assert types == ["model", "tool", "model", "final"]
            assert body["steps"][1]["tool"] == "echo.ping"
            assert body["steps"][1]["args"] == {"message": "hello"}
            assert body["result"] == "hello"
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_cancel_transitions_to_cancelled(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path, max_steps=10, timeout_seconds=30)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="fixture-echo",
                    project_id="proj-a",
                    run_input="cancel-me",
                    context={"dry_run": True, "decide_delay_seconds": 2},
                )
            )
            await asyncio.sleep(0.05)
            outcome = engine.store.request_cancel(run.id)
            assert outcome == "cancelling"
            engine.store.finish_run(run.id, status="cancelled", error="cancelled")
            body = await _wait_terminal(engine, run.id)
            assert body["status"] == "cancelled"
            assert body["error"] == "cancelled"
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())


def test_plan_context_drives_multi_step_script(tmp_path: Path) -> None:
    async def _run() -> None:
        engine = _engine(tmp_path, max_steps=6, timeout_seconds=30)
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="fixture-echo",
                    project_id="proj-a",
                    run_input="ignored",
                    context={
                        "dry_run": True,
                        "plan": [
                            {
                                "kind": "tool_call",
                                "tool": "echo.ping",
                                "args": {"message": "step-a"},
                            },
                            {
                                "kind": "tool_call",
                                "tool": "echo.ping",
                                "args": {"message": "step-b"},
                            },
                            {"kind": "final", "text": "done-via-plan"},
                        ],
                    },
                )
            )
            body = await _wait_terminal(engine, run.id)
            assert body["status"] == "succeeded"
            assert body["result"] == "done-via-plan"
            tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
            assert [s["args"]["message"] for s in tool_steps] == ["step-a", "step-b"]
        finally:
            await engine.aclose()
            engine.store.close()

    asyncio.run(_run())
