"""Contract tests for forge-agents OpenAPI (agent + tool registry schemas)."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient
from jsonschema import Draft202012Validator


def _repo_openapi() -> Path:
    """Resolve canonical OpenAPI when the full repo tree is present."""
    here = Path(__file__).resolve()
    # Local checkout: services/forge-agents/tests → repo root is parents[3]
    if len(here.parents) > 3:
        candidate = here.parents[3] / "contracts" / "openapi" / "forge-agents.openapi.yaml"
        if candidate.is_file():
            return candidate
    # Walk up looking for contracts/openapi/...
    for parent in here.parents:
        candidate = parent / "contracts" / "openapi" / "forge-agents.openapi.yaml"
        if candidate.is_file():
            return candidate
    return (
        here.parents[min(3, len(here.parents) - 1)]
        / "contracts"
        / "openapi"
        / "forge-agents.openapi.yaml"
    )


OPENAPI = _repo_openapi()


def test_openapi_documents_agent_and_tool_schemas() -> None:
    if not OPENAPI.is_file():
        pytest.skip(f"canonical OpenAPI not in build context ({OPENAPI})")
    doc = yaml.safe_load(OPENAPI.read_text(encoding="utf-8"))
    paths = doc["paths"]
    assert "/health/live" in paths
    assert "/health/ready" in paths
    assert "/" in paths
    assert "/v1/agents" in paths
    assert "/v1/agents/{name}" in paths
    assert "/v1/tools" in paths
    assert "/v1/agents/{name}/runs" in paths
    assert "/v1/runs" in paths
    assert "/v1/runs/{run_id}" in paths
    assert "/v1/runs/{run_id}/cancel" in paths

    list_op = paths["/v1/agents"]["get"]
    assert "200" in list_op["responses"]
    get_op = paths["/v1/agents/{name}"]["get"]
    assert "200" in get_op["responses"]
    assert "404" in get_op["responses"]
    tools_op = paths["/v1/tools"]["get"]
    assert "200" in tools_op["responses"]
    start_op = paths["/v1/agents/{name}/runs"]["post"]
    assert "202" in start_op["responses"]
    get_run = paths["/v1/runs/{run_id}"]["get"]
    assert "200" in get_run["responses"]
    assert "404" in get_run["responses"]
    cancel_op = paths["/v1/runs/{run_id}/cancel"]["post"]
    assert "200" in cancel_op["responses"]
    assert "409" in cancel_op["responses"]

    schemas = doc["components"]["schemas"]
    assert "Agent" in schemas
    assert "AgentListResponse" in schemas
    assert "AgentLimits" in schemas
    assert "Tool" in schemas
    assert "ToolListResponse" in schemas
    assert "ToolInvokeDenialReason" in schemas
    assert "ErrorBody" in schemas
    assert "Run" in schemas
    assert "RunStep" in schemas
    assert "RunListResponse" in schemas
    assert "StartRunRequest" in schemas
    run_step = schemas["RunStep"]
    assert set(run_step["required"]) >= {"type", "ts"}
    assert set(run_step["properties"]["type"]["enum"]) == {"model", "tool", "final"}
    agent = schemas["Agent"]
    assert set(agent["required"]) >= {
        "name",
        "model",
        "tools",
        "permissions",
        "limits",
    }
    limits = schemas["AgentLimits"]
    assert set(limits["required"]) >= {"max_steps", "timeout_seconds"}
    assert limits["properties"]["max_steps"]["maximum"] == 100
    assert limits["properties"]["timeout_seconds"]["maximum"] == 3600

    tool = schemas["Tool"]
    assert set(tool["required"]) >= {
        "name",
        "input_schema",
        "output_schema",
        "destructive",
        "required_permissions",
    }
    reasons = set(schemas["ToolInvokeDenialReason"]["enum"])
    assert reasons == {
        "unknown_tool",
        "not_declared",
        "permission_denied",
        "invalid_arguments",
    }


def test_list_and_get_responses_match_schema(client: TestClient) -> None:
    listed = client.get("/v1/agents")
    assert listed.status_code == 200
    body = listed.json()
    assert isinstance(body.get("agents"), list)
    assert body["agents"], "expected packaged fixture agent"
    for agent in body["agents"]:
        for key in ("name", "model", "tools", "permissions", "limits"):
            assert key in agent
        assert "max_steps" in agent["limits"]
        assert "timeout_seconds" in agent["limits"]

    name = body["agents"][0]["name"]
    detail = client.get(f"/v1/agents/{name}")
    assert detail.status_code == 200
    assert detail.json()["name"] == name

    missing = client.get("/v1/agents/nope")
    assert missing.status_code == 404
    err = missing.json()
    assert err["code"] == "agent_not_found"
    assert "error" in err

    tools = client.get("/v1/tools")
    assert tools.status_code == 200
    tool_body = tools.json()
    assert isinstance(tool_body.get("tools"), list)
    assert tool_body["tools"], "expected fake tools"
    for tool in tool_body["tools"]:
        for key in (
            "name",
            "input_schema",
            "output_schema",
            "destructive",
            "required_permissions",
        ):
            assert key in tool
        Draft202012Validator.check_schema(tool["input_schema"])
        Draft202012Validator.check_schema(tool["output_schema"])


def test_live_ready_identity_match_contract(client: TestClient) -> None:
    live = client.get("/health/live")
    ready = client.get("/health/ready")
    identity = client.get("/")
    assert live.status_code == 200 and live.json() == {"status": "live"}
    assert ready.status_code == 200 and ready.json() == {"status": "ready"}
    body = identity.json()
    for key in ("service", "language", "status", "version"):
        assert key in body


def test_invalid_models_url_fails_create_app(clean_env: pytest.MonkeyPatch) -> None:
    from pydantic import ValidationError

    from app.main import create_app

    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_MODELS_URL", "bogus")
    with pytest.raises(ValidationError):
        create_app()
