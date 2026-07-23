"""Pydantic models for declarative agent YAML definitions."""

from __future__ import annotations

import re

from pydantic import BaseModel, ConfigDict, Field, field_validator

# Spec examples: deployment.read, logs.search, runtime.restart
_TOOL_RE = re.compile(r"^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$")
# Spec examples: project:read, deployment:read
_PERMISSION_RE = re.compile(r"^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$")
# Spec examples: deployment-investigator, fixture-echo
_NAME_RE = re.compile(r"^[a-z][a-z0-9_-]*$")
# Spec examples: local-general
_MODEL_RE = re.compile(r"^[a-z][a-z0-9_./-]*$")

# Bounds reject absurd values while allowing the documented defaults (10 / 120).
MAX_STEPS_MIN = 1
MAX_STEPS_MAX = 100
TIMEOUT_SECONDS_MIN = 1
TIMEOUT_SECONDS_MAX = 3600


class AgentLimits(BaseModel):
    """Hard execution ceilings declared on an agent."""

    model_config = ConfigDict(extra="forbid")

    max_steps: int = Field(..., ge=MAX_STEPS_MIN, le=MAX_STEPS_MAX)
    timeout_seconds: int = Field(..., ge=TIMEOUT_SECONDS_MIN, le=TIMEOUT_SECONDS_MAX)


class AgentDefinition(BaseModel):
    """Validated agent definition loaded from YAML."""

    model_config = ConfigDict(extra="forbid")

    name: str = Field(..., min_length=1)
    model: str = Field(..., min_length=1)
    tools: list[str] = Field(default_factory=list)
    permissions: list[str] = Field(default_factory=list)
    limits: AgentLimits

    @field_validator("name")
    @classmethod
    def validate_name(cls, value: str) -> str:
        name = value.strip()
        if not _NAME_RE.fullmatch(name):
            raise ValueError(
                "name must be a non-empty lowercase identifier "
                "(letters, digits, '_', '-'; e.g. fixture-echo)"
            )
        return name

    @field_validator("model")
    @classmethod
    def validate_model(cls, value: str) -> str:
        model = value.strip()
        if not _MODEL_RE.fullmatch(model):
            raise ValueError("model must be a non-empty identifier (e.g. local-general)")
        return model

    @field_validator("tools")
    @classmethod
    def validate_tools(cls, value: list[str]) -> list[str]:
        if not isinstance(value, list):
            raise ValueError("tools must be a list of strings")
        cleaned: list[str] = []
        seen: set[str] = set()
        for item in value:
            if not isinstance(item, str) or not item.strip():
                raise ValueError("tools entries must be non-empty strings")
            tool = item.strip()
            if not _TOOL_RE.fullmatch(tool):
                raise ValueError(
                    f"tool '{tool}' is malformed (expected dotted id like deployment.read)"
                )
            if tool in seen:
                raise ValueError(f"duplicate tool '{tool}'")
            seen.add(tool)
            cleaned.append(tool)
        return cleaned

    @field_validator("permissions")
    @classmethod
    def validate_permissions(cls, value: list[str]) -> list[str]:
        if not isinstance(value, list):
            raise ValueError("permissions must be a list of strings")
        cleaned: list[str] = []
        seen: set[str] = set()
        for item in value:
            if not isinstance(item, str) or not item.strip():
                raise ValueError("permissions entries must be non-empty strings")
            perm = item.strip()
            if not _PERMISSION_RE.fullmatch(perm):
                raise ValueError(
                    f"permission '{perm}' is malformed (expected scoped id like project:read)"
                )
            if perm in seen:
                raise ValueError(f"duplicate permission '{perm}'")
            seen.add(perm)
            cleaned.append(perm)
        return cleaned

    def to_api_dict(self) -> dict[str, object]:
        """Serialize for GET /v1/agents responses."""
        return {
            "name": self.name,
            "model": self.model,
            "tools": list(self.tools),
            "permissions": list(self.permissions),
            "limits": {
                "max_steps": self.limits.max_steps,
                "timeout_seconds": self.limits.timeout_seconds,
            },
        }
