"""Unit tests for AgentDefinition pydantic validation."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.agents.models import AgentDefinition


def _valid(**overrides: object) -> dict:
    base: dict = {
        "name": "fixture-echo",
        "model": "local-general",
        "tools": ["echo.ping"],
        "permissions": ["project:read"],
        "limits": {"max_steps": 3, "timeout_seconds": 30},
    }
    base.update(overrides)
    return base


def test_valid_yaml_shape_parses() -> None:
    agent = AgentDefinition.model_validate(_valid())
    assert agent.name == "fixture-echo"
    assert agent.model == "local-general"
    assert agent.tools == ["echo.ping"]
    assert agent.permissions == ["project:read"]
    assert agent.limits.max_steps == 3
    assert agent.limits.timeout_seconds == 30


def test_rejects_unknown_fields() -> None:
    with pytest.raises(ValidationError):
        AgentDefinition.model_validate(_valid(secret="nope"))


def test_rejects_malformed_tool() -> None:
    with pytest.raises(ValidationError) as exc:
        AgentDefinition.model_validate(_valid(tools=["not a tool"]))
    assert "tool" in str(exc.value).lower()


def test_rejects_malformed_permission() -> None:
    with pytest.raises(ValidationError) as exc:
        AgentDefinition.model_validate(_valid(permissions=["read-all"]))
    assert "permission" in str(exc.value).lower()


def test_rejects_max_steps_out_of_bounds() -> None:
    with pytest.raises(ValidationError):
        AgentDefinition.model_validate(_valid(limits={"max_steps": 0, "timeout_seconds": 30}))
    with pytest.raises(ValidationError):
        AgentDefinition.model_validate(_valid(limits={"max_steps": 101, "timeout_seconds": 30}))


def test_rejects_timeout_out_of_bounds() -> None:
    with pytest.raises(ValidationError):
        AgentDefinition.model_validate(_valid(limits={"max_steps": 3, "timeout_seconds": 0}))
    with pytest.raises(ValidationError):
        AgentDefinition.model_validate(_valid(limits={"max_steps": 3, "timeout_seconds": 3601}))
