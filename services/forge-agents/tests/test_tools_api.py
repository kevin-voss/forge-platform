"""Integration tests for GET /v1/tools and invoker via app state."""

from __future__ import annotations

import asyncio
from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient
from jsonschema import Draft202012Validator

from app.config import clear_settings_cache
from app.main import create_app
from app.permissions import CallScope
from app.tools.invoker import (
    REASON_INVALID_ARGUMENTS,
    REASON_NOT_DECLARED,
    REASON_PERMISSION_DENIED,
    REASON_UNKNOWN_TOOL,
    ToolInvoker,
)


def _write_agent(directory: Path, filename: str, payload: object) -> None:
    (directory / filename).write_text(yaml.safe_dump(payload), encoding="utf-8")


@pytest.fixture
def tools_client(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
    _write_agent(
        tmp_path,
        "fixture-echo.yaml",
        {
            "name": "fixture-echo",
            "model": "local-general",
            "tools": ["echo.ping", "deployment.read"],
            "permissions": ["project:read", "deployment:read"],
            "limits": {"max_steps": 3, "timeout_seconds": 30},
        },
    )
    monkeypatch.setenv("PORT", "4301")
    monkeypatch.setenv("FORGE_LOG_LEVEL", "error")
    monkeypatch.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    monkeypatch.setenv("FORGE_AGENTS_DEFS_DIR", str(tmp_path))
    monkeypatch.setenv("FORGE_AGENTS_TOOLS_MODE", "fake")
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as test_client:
        yield test_client
    clear_settings_cache()


def test_list_tools_includes_fake_schemas(tools_client: TestClient) -> None:
    resp = tools_client.get("/v1/tools")
    assert resp.status_code == 200
    body = resp.json()
    assert "tools" in body
    by_name = {t["name"]: t for t in body["tools"]}
    assert "echo.ping" in by_name
    assert "fail.raise" in by_name
    assert "deployment.read" in by_name

    echo = by_name["echo.ping"]
    for key in (
        "name",
        "input_schema",
        "output_schema",
        "destructive",
        "required_permissions",
    ):
        assert key in echo
    assert echo["destructive"] is False
    assert "project:read" in echo["required_permissions"]
    Draft202012Validator.check_schema(echo["input_schema"])
    Draft202012Validator.check_schema(echo["output_schema"])


def test_default_client_lists_tools(client: TestClient) -> None:
    resp = client.get("/v1/tools")
    assert resp.status_code == 200
    names = {t["name"] for t in resp.json()["tools"]}
    assert "echo.ping" in names
    assert "required_permissions" in resp.json()["tools"][0]


def _invoker(client: TestClient) -> ToolInvoker:
    return client.app.state.tool_invoker


def test_invoke_allowed_via_app_invoker(tools_client: TestClient) -> None:
    agent = tools_client.app.state.registry.get("fixture-echo")
    assert agent is not None
    scope = CallScope.from_permissions(agent.permissions, project_id="proj-1")
    result = asyncio.run(
        _invoker(tools_client).invoke(
            agent=agent,
            tool_name="echo.ping",
            arguments={"message": "ping"},
            scope=scope,
        )
    )
    assert result.ok is True
    assert result.output == {"echo": "ping"}


def test_invoke_denial_reasons_via_app_invoker(tools_client: TestClient) -> None:
    agent = tools_client.app.state.registry.get("fixture-echo")
    assert agent is not None
    invoker = _invoker(tools_client)

    unknown = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="ghost.tool",
            arguments={},
            scope=CallScope.from_permissions(agent.permissions),
        )
    )
    assert unknown.reason == REASON_UNKNOWN_TOOL

    # Restrict agent tools for not_declared (clone-like override via model_copy).
    narrow = agent.model_copy(update={"tools": ["echo.ping"]})
    undeclared = asyncio.run(
        invoker.invoke(
            agent=narrow,
            tool_name="deployment.read",
            arguments={"deployment_id": "d1"},
            scope=CallScope.from_permissions(["deployment:read"]),
        )
    )
    assert undeclared.reason == REASON_NOT_DECLARED

    denied = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="deployment.read",
            arguments={"deployment_id": "d1"},
            scope=CallScope.from_permissions(["project:read"]),
        )
    )
    assert denied.reason == REASON_PERMISSION_DENIED

    bad_args = asyncio.run(
        invoker.invoke(
            agent=agent,
            tool_name="echo.ping",
            arguments={"message": 1},
            scope=CallScope.from_permissions(["project:read"]),
        )
    )
    assert bad_args.reason == REASON_INVALID_ARGUMENTS
