"""Unit + integration tests for platform tools (15.05)."""

from __future__ import annotations

import asyncio
from pathlib import Path

import httpx
import pytest
import yaml
from fastapi.testclient import TestClient
from jsonschema import Draft202012Validator

from app.agents.models import AgentDefinition, AgentLimits
from app.permissions import CallScope
from app.run.engine import RunEngine, StartRunRequest
from app.run.model_client import FakeModelClient
from app.run.store import RunStore
from app.tools.control import DeploymentReadTool
from app.tools.errors import ERROR_BACKEND_UNAVAILABLE, ERROR_TOOL_TIMEOUT, ToolError
from app.tools.http_backend import HttpBackend
from app.tools.invoker import ToolInvoker
from app.tools.observe import LogsSearchTool
from app.tools.registry import build_tool_registry


PLATFORM_TOOLS = [
    "deployment.read",
    "logs.search",
    "metrics.query",
    "runtime.restart",
    "storage.get",
    "storage.put",
    "models.generate",
    "models.embed",
    "events.publish",
    "memory.search",
    "memory.upsert",
]


def _agent_for(tools: list[str], permissions: list[str]) -> AgentDefinition:
    return AgentDefinition(
        name="platform-tester",
        model="local-general",
        tools=tools,
        permissions=permissions,
        limits=AgentLimits(max_steps=8, timeout_seconds=30),
    )


def test_registry_lists_platform_tools_and_destructive_flag() -> None:
    registry = build_tool_registry("fake")
    by_name = {t.name: t for t in registry.list()}
    for name in PLATFORM_TOOLS:
        assert name in by_name
        Draft202012Validator.check_schema(by_name[name].input_schema)
        Draft202012Validator.check_schema(by_name[name].output_schema)
    assert by_name["runtime.restart"].destructive is True
    assert by_name["deployment.read"].destructive is False
    assert "runtime:restart" in by_name["runtime.restart"].required_permissions


@pytest.mark.parametrize(
    ("tool_name", "args", "permissions"),
    [
        ("deployment.read", {"deployment_id": "dep-1"}, ["deployment:read"]),
        ("logs.search", {"deployment": "dep-1"}, ["logs:read"]),
        ("metrics.query", {"query": "up"}, ["metrics:read"]),
        ("runtime.restart", {"deployment_id": "dep-1"}, ["runtime:restart"]),
        ("storage.get", {"bucket": "b", "key": "k"}, ["storage:read"]),
        ("storage.put", {"bucket": "b", "key": "k", "content": "x"}, ["storage:write"]),
        (
            "models.generate",
            {"model": "local-general", "prompt": "hi"},
            ["models:generate"],
        ),
        (
            "models.embed",
            {"model": "local-embed-small", "input": "hi"},
            ["models:embed"],
        ),
        (
            "events.publish",
            {"subject": "application.diagnosed", "data": {"ok": True}},
            ["events:publish"],
        ),
        (
            "memory.search",
            {"collection": "incidents", "query": "db timeout", "top_k": 3},
            ["memory:read"],
        ),
        (
            "memory.upsert",
            {
                "collection": "incidents",
                "items": [{"id": "n1", "text": "note", "metadata": {}}],
            },
            ["memory:write"],
        ),
    ],
)
def test_fake_tools_deterministic(
    tool_name: str,
    args: dict,
    permissions: list[str],
) -> None:
    registry = build_tool_registry("fake")
    invoker = ToolInvoker(registry)
    agent = _agent_for([tool_name], permissions)
    scope = CallScope.from_permissions(permissions, project_id="proj-a")
    first = asyncio.run(
        invoker.invoke(agent=agent, tool_name=tool_name, arguments=args, scope=scope)
    )
    second = asyncio.run(
        invoker.invoke(agent=agent, tool_name=tool_name, arguments=args, scope=scope)
    )
    assert first.ok is True
    assert first.output == second.output
    assert first.output is not None


def test_runtime_restart_listed_destructive_via_api(client: TestClient) -> None:
    resp = client.get("/v1/tools")
    assert resp.status_code == 200
    tools = resp.json()["tools"]
    restart = next(t for t in tools if t["name"] == "runtime.restart")
    assert restart["destructive"] is True
    assert "runtime:restart" in restart["required_permissions"]


def test_live_backend_unavailable_normalized() -> None:
    transport = httpx.MockTransport(
        lambda request: (_ for _ in ()).throw(httpx.ConnectError("down"))
    )
    backend = HttpBackend(
        "http://control.test",
        timeout_seconds=1.0,
        client=httpx.AsyncClient(transport=transport, base_url="http://control.test"),
        service="control",
    )
    tool = DeploymentReadTool(mode="live", backend=backend)

    async def _run() -> None:
        with pytest.raises(ToolError) as exc:
            await tool.execute({"deployment_id": "dep-1"})
        assert exc.value.error_code == ERROR_BACKEND_UNAVAILABLE

    asyncio.run(_run())
    asyncio.run(backend.aclose())


def test_live_timeout_normalized() -> None:
    def _handler(request: httpx.Request) -> httpx.Response:
        raise httpx.TimeoutException("slow", request=request)

    transport = httpx.MockTransport(_handler)
    backend = HttpBackend(
        "http://observe.test",
        timeout_seconds=0.1,
        client=httpx.AsyncClient(transport=transport, base_url="http://observe.test"),
        service="observe",
    )
    tool = LogsSearchTool(mode="live", backend=backend)

    async def _run() -> None:
        with pytest.raises(ToolError) as exc:
            await tool.execute({"deployment": "dep-1"})
        assert exc.value.error_code == ERROR_TOOL_TIMEOUT

    asyncio.run(_run())
    asyncio.run(backend.aclose())


def test_invoker_records_normalized_backend_error_without_crash() -> None:
    transport = httpx.MockTransport(
        lambda request: (_ for _ in ()).throw(httpx.ConnectError("down"))
    )
    # Build registry then swap deployment.read with a live tool using mock transport.
    registry = build_tool_registry("fake")
    backend = HttpBackend(
        "http://control.test",
        client=httpx.AsyncClient(transport=transport, base_url="http://control.test"),
        service="control",
    )
    registry.tools["deployment.read"] = DeploymentReadTool(mode="live", backend=backend)
    invoker = ToolInvoker(registry)
    agent = _agent_for(["deployment.read"], ["deployment:read"])
    result = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="deployment.read",
            arguments={"deployment_id": "dep-1"},
            scope=CallScope.from_permissions(["deployment:read"], project_id="p"),
        )
    )
    assert result.ok is False
    assert result.reason == ERROR_BACKEND_UNAVAILABLE
    assert result.output is not None
    assert result.output["tool"] == "deployment.read"
    assert result.output["error_code"] == ERROR_BACKEND_UNAVAILABLE
    assert "message" in result.output
    asyncio.run(backend.aclose())


def test_run_with_read_tools_fake_mode(tmp_path: Path) -> None:
    payload = {
        "name": "investigator",
        "model": "local-general",
        "tools": ["deployment.read", "logs.search", "metrics.query"],
        "permissions": ["deployment:read", "logs:read", "metrics:read"],
        "limits": {"max_steps": 6, "timeout_seconds": 30},
    }
    (tmp_path / "investigator.yaml").write_text(yaml.safe_dump(payload), encoding="utf-8")
    from app.agents.loader import load_registry
    from app.permissions import PermissionChecker

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
                    agent_name="investigator",
                    project_id="proj-a",
                    run_input="investigate",
                    context={
                        "dry_run": True,
                        "tool": "deployment.read",
                        "deployment_id": "dep-fixture",
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
    assert tool_steps[0]["tool"] == "deployment.read"
    assert tool_steps[0]["observation"]["ok"] is True
    assert tool_steps[0]["observation"]["deployment_id"] == "dep-fixture"


def test_openapi_tools_example_mentions_destructive(client: TestClient) -> None:
    resp = client.get("/v1/tools")
    names = {t["name"] for t in resp.json()["tools"]}
    assert "runtime.restart" in names
    assert "logs.search" in names
    assert "metrics.query" in names


def test_list_tools_error_shape_contract(client: TestClient) -> None:
    """Contract: normalized tool errors expose tool/error_code/message keys."""
    invoker = client.app.state.tool_invoker
    fail_agent = AgentDefinition(
        name="failer",
        model="local-general",
        tools=["fail.raise"],
        permissions=["project:read"],
        limits=AgentLimits(max_steps=2, timeout_seconds=10),
    )
    result = asyncio.run(
        invoker.invoke(
            agent=fail_agent,
            tool_name="fail.raise",
            arguments={"reason": "boom"},
            scope=CallScope.from_permissions(["project:read"]),
        )
    )
    assert result.ok is False
    assert result.output is not None
    assert set(result.output) >= {"tool", "error_code", "message"}
    assert result.output["tool"] == "fail.raise"
