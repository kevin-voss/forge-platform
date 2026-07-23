"""Integration tests for GET /v1/agents registry routes."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

from app.agents.loader import AgentLoadError
from app.config import clear_settings_cache
from app.main import create_app


def _write_agent(directory: Path, filename: str, payload: object) -> None:
    (directory / filename).write_text(yaml.safe_dump(payload), encoding="utf-8")


@pytest.fixture
def registry_client(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
    _write_agent(
        tmp_path,
        "fixture-echo.yaml",
        {
            "name": "fixture-echo",
            "model": "local-general",
            "tools": ["echo.ping"],
            "permissions": ["project:read"],
            "limits": {"max_steps": 3, "timeout_seconds": 30},
        },
    )
    monkeypatch.setenv("PORT", "4301")
    monkeypatch.setenv("FORGE_LOG_LEVEL", "error")
    monkeypatch.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    monkeypatch.setenv("FORGE_AGENTS_DEFS_DIR", str(tmp_path))
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as test_client:
        yield test_client
    clear_settings_cache()


def test_list_agents_includes_fixture(registry_client: TestClient) -> None:
    resp = registry_client.get("/v1/agents")
    assert resp.status_code == 200
    body = resp.json()
    assert "agents" in body
    names = {agent["name"] for agent in body["agents"]}
    assert "fixture-echo" in names
    echo = next(a for a in body["agents"] if a["name"] == "fixture-echo")
    assert echo["model"] == "local-general"
    assert echo["tools"] == ["echo.ping"]
    assert echo["permissions"] == ["project:read"]
    assert echo["limits"] == {"max_steps": 3, "timeout_seconds": 30}


def test_get_agent_by_name(registry_client: TestClient) -> None:
    resp = registry_client.get("/v1/agents/fixture-echo")
    assert resp.status_code == 200
    assert resp.json()["name"] == "fixture-echo"


def test_get_unknown_agent_404(registry_client: TestClient) -> None:
    resp = registry_client.get("/v1/agents/unknown")
    assert resp.status_code == 404
    body = resp.json()
    assert body["code"] == "agent_not_found"
    assert "unknown" in body["error"]


def test_default_client_lists_packaged_fixture(client: TestClient) -> None:
    resp = client.get("/v1/agents")
    assert resp.status_code == 200
    names = {agent["name"] for agent in resp.json()["agents"]}
    assert "fixture-echo" in names


def test_bad_defs_dir_fails_create_app(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    bad = tmp_path / "bad.yaml"
    bad.write_text("name: [\n", encoding="utf-8")
    monkeypatch.setenv("PORT", "4301")
    monkeypatch.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    monkeypatch.setenv("FORGE_AGENTS_DEFS_DIR", str(tmp_path))
    clear_settings_cache()
    with pytest.raises(AgentLoadError):
        create_app()
    clear_settings_cache()
