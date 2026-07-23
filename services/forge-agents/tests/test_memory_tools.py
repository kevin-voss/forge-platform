"""Unit + integration tests for memory.search / memory.upsert (17.05)."""

from __future__ import annotations

import asyncio
from pathlib import Path

import pytest
import yaml
from jsonschema import Draft202012Validator

from app.agents.models import AgentDefinition, AgentLimits
from app.permissions import CallScope, PermissionChecker
from app.run.engine import RunEngine, StartRunRequest
from app.run.model_client import FakeModelClient
from app.run.store import RunStore
from app.tools.invoker import ToolInvoker
from app.tools.memory import (
    MemorySearchTool,
    MemoryUpsertTool,
    memory_tool_calls_snapshot,
    reset_memory_tool_calls,
)
from app.tools.registry import build_tool_registry


def test_memory_tool_schemas_valid() -> None:
    registry = build_tool_registry("fake")
    by_name = {t.name: t for t in registry.list()}
    for name in ("memory.search", "memory.upsert"):
        Draft202012Validator.check_schema(by_name[name].input_schema)
        Draft202012Validator.check_schema(by_name[name].output_schema)
    assert by_name["memory.search"].required_permissions == ["memory:read"]
    assert by_name["memory.upsert"].required_permissions == ["memory:write"]
    assert by_name["memory.upsert"].destructive is False


def test_memory_search_maps_fixture_results() -> None:
    reset_memory_tool_calls()
    tool = MemorySearchTool(mode="fake")
    result = asyncio.run(
        tool.execute({"collection": "incidents", "query": "connection refused", "top_k": 2})
    )
    assert "results" in result.output
    assert result.output["results"][0]["id"] == "incident-db-timeout"
    assert result.output["results"][0]["score"] >= 0.9
    assert "metadata" in result.output["results"][0]
    assert any(row["op"] == "search" for row in memory_tool_calls_snapshot())


def test_memory_upsert_requires_write_permission() -> None:
    registry = build_tool_registry("fake")
    invoker = ToolInvoker(registry, checker=PermissionChecker())
    agent = AgentDefinition(
        name="mem-writer",
        model="local-general",
        tools=["memory.upsert"],
        permissions=["memory:write"],
        limits=AgentLimits(max_steps=2, timeout_seconds=10),
    )
    denied = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="memory.upsert",
            arguments={
                "collection": "incidents",
                "items": [{"id": "x", "text": "y"}],
            },
            scope=CallScope.from_permissions(["memory:read"], project_id="proj-a"),
        )
    )
    assert denied.ok is False
    assert denied.reason == "permission_denied"

    allowed = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="memory.upsert",
            arguments={
                "collection": "incidents",
                "items": [{"id": "x", "text": "y"}],
            },
            scope=CallScope.from_permissions(["memory:write"], project_id="proj-a"),
        )
    )
    assert allowed.ok is True
    assert allowed.output is not None
    assert allowed.output["upserted"] == 1


def test_agent_run_memory_search_cites_records(tmp_path: Path) -> None:
    payload = {
        "name": "memory-citer",
        "model": "local-general",
        "tools": ["memory.search"],
        "permissions": ["memory:read"],
        "limits": {"max_steps": 6, "timeout_seconds": 30},
    }
    (tmp_path / "memory-citer.yaml").write_text(yaml.safe_dump(payload), encoding="utf-8")
    from app.agents.loader import load_registry

    registry = load_registry(str(tmp_path))
    store = RunStore(tmp_path / "runs.db")
    invoker = ToolInvoker(build_tool_registry("fake"), checker=PermissionChecker())
    engine = RunEngine(
        store=store,
        registry=registry,
        invoker=invoker,
        model_client=FakeModelClient(),
        fake_model_client=FakeModelClient(),
        max_concurrent_runs=2,
    )

    async def _run() -> dict:
        try:
            run = await engine.start(
                StartRunRequest(
                    agent_name="memory-citer",
                    project_id="proj-a",
                    run_input="database connection refused",
                    context={
                        "dry_run": True,
                        "tool": "memory.search",
                        "collection": "incidents",
                        "query": "database connection refused",
                        "top_k": 3,
                    },
                )
            )
            for _ in range(100):
                body = engine.store.get_run(run.id)
                assert body is not None
                if body.status != "running":
                    return body.to_api_dict()
                await asyncio.sleep(0.02)
            raise AssertionError("run did not finish")
        finally:
            await engine.aclose()
            store.close()

    body = asyncio.run(_run())
    assert body["status"] == "succeeded"
    tool_steps = [s for s in body["steps"] if s["type"] == "tool"]
    assert tool_steps
    assert tool_steps[0]["tool"] == "memory.search"
    obs = tool_steps[0]["observation"]
    assert obs["ok"] is True
    assert obs["results"][0]["id"] == "incident-db-timeout"
    # Final answer includes retrieved record ids for citation.
    assert "incident-db-timeout" in str(body.get("result") or "")
    finals = [s for s in body["steps"] if s["type"] == "final"]
    assert finals
    assert "incident-db-timeout" in str(finals[0].get("observation") or "")


def test_memory_upsert_tool_direct_fake() -> None:
    tool = MemoryUpsertTool(mode="fake")
    result = asyncio.run(
        tool.execute(
            {
                "collection": "incidents",
                "items": [{"id": "a", "text": "hello"}, {"id": "b", "text": "world"}],
            }
        )
    )
    assert result.output["collection"] == "incidents"
    assert result.output["upserted"] == 2
