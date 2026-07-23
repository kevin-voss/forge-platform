"""Unit tests for ToolInvoker enforcement paths."""

from __future__ import annotations

import asyncio

import pytest

from app.agents.models import AgentDefinition, AgentLimits
from app.permissions import CallScope
from app.tools.invoker import (
    REASON_INVALID_ARGUMENTS,
    REASON_NOT_DECLARED,
    REASON_PERMISSION_DENIED,
    REASON_UNKNOWN_TOOL,
    ToolInvoker,
)
from app.tools.metrics import ToolMetrics
from app.tools.registry import build_tool_registry


@pytest.fixture
def invoker() -> ToolInvoker:
    metrics = ToolMetrics()
    return ToolInvoker(build_tool_registry("fake"), metrics=metrics)


def _agent(*, tools: list[str], permissions: list[str] | None = None) -> AgentDefinition:
    return AgentDefinition(
        name="fixture-echo",
        model="local-general",
        tools=tools,
        permissions=permissions or ["project:read"],
        limits=AgentLimits(max_steps=3, timeout_seconds=30),
    )


def test_allows_valid_call(invoker: ToolInvoker) -> None:
    agent = _agent(tools=["echo.ping"])
    scope = CallScope.from_permissions(["project:read"], project_id="p1")
    result = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="echo.ping",
            arguments={"message": "hello"},
            scope=scope,
        )
    )
    assert result.ok is True
    assert result.decision == "allow"
    assert result.reason is None
    assert result.output == {"echo": "hello"}


def test_rejects_unknown_tool(invoker: ToolInvoker) -> None:
    agent = _agent(tools=["echo.ping", "made.up"])
    scope = CallScope.from_permissions(["project:read"])
    result = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="made.up",
            arguments={},
            scope=scope,
        )
    )
    assert result.ok is False
    assert result.decision == "deny"
    assert result.reason == REASON_UNKNOWN_TOOL


def test_rejects_not_declared(invoker: ToolInvoker) -> None:
    agent = _agent(tools=["echo.ping"])
    scope = CallScope.from_permissions(["project:read", "deployment:read"])
    result = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="deployment.read",
            arguments={"deployment_id": "d1"},
            scope=scope,
        )
    )
    assert result.ok is False
    assert result.reason == REASON_NOT_DECLARED


def test_unknown_before_not_declared(invoker: ToolInvoker) -> None:
    """Hallucinated names (not in registry) are unknown_tool, not not_declared."""
    agent = _agent(tools=["echo.ping"])
    scope = CallScope.from_permissions(["project:read"])
    result = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="hallucinated.tool",
            arguments={},
            scope=scope,
        )
    )
    assert result.reason == REASON_UNKNOWN_TOOL


def test_rejects_permission_denied(invoker: ToolInvoker) -> None:
    agent = _agent(tools=["deployment.read"], permissions=["deployment:read"])
    scope = CallScope.from_permissions(["project:read"])  # missing deployment:read
    result = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="deployment.read",
            arguments={"deployment_id": "d1"},
            scope=scope,
        )
    )
    assert result.ok is False
    assert result.reason == REASON_PERMISSION_DENIED
    assert result.error is not None
    assert "deployment:read" in result.error


def test_rejects_invalid_arguments(invoker: ToolInvoker) -> None:
    agent = _agent(tools=["echo.ping"])
    scope = CallScope.from_permissions(["project:read"])
    result = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="echo.ping",
            arguments={},  # missing required message
            scope=scope,
        )
    )
    assert result.ok is False
    assert result.reason == REASON_INVALID_ARGUMENTS


def test_metrics_record_allow_and_deny(invoker: ToolInvoker) -> None:
    agent = _agent(tools=["echo.ping"])
    scope = CallScope.from_permissions(["project:read"])
    asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="echo.ping",
            arguments={"message": "ok"},
            scope=scope,
        )
    )
    asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="no.such",
            arguments={},
            scope=scope,
        )
    )
    snap = invoker._metrics.snapshot()  # noqa: SLF001 — test introspection
    calls = {
        (c["tool"], c["decision"]): c["value"]
        for c in snap["agent_tool_calls_total"]  # type: ignore[index]
    }
    assert calls.get(("echo.ping", "allow"), 0) >= 1
    assert calls.get(("no.such", "deny"), 0) >= 1
    denied = {d["reason"]: d["value"] for d in snap["agent_tool_denied_total"]}  # type: ignore[index]
    assert denied.get(REASON_UNKNOWN_TOOL, 0) >= 1
