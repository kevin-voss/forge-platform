"""Deterministic fake/mock tools for CI and unit tests (real tools in 15.05)."""

from __future__ import annotations

from typing import Any

from app.tools.base import Tool, ToolResult


class EchoPingTool(Tool):
    """Echoes a message; used by the fixture-echo agent."""

    name = "echo.ping"
    destructive = False
    required_permissions = ["project:read"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "message": {"type": "string", "minLength": 1},
        },
        "required": ["message"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "echo": {"type": "string"},
        },
        "required": ["echo"],
    }

    async def execute(self, args: dict[str, Any]) -> ToolResult:
        return ToolResult(output={"echo": str(args["message"])})


class FailRaiseTool(Tool):
    """Always fails at execute time (for run-engine failure-path tests later)."""

    name = "fail.raise"
    destructive = False
    required_permissions = ["project:read"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "reason": {"type": "string"},
        },
        "required": [],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "ok": {"type": "boolean"},
        },
        "required": ["ok"],
    }

    async def execute(self, args: dict[str, Any]) -> ToolResult:
        reason = args.get("reason") or "fail.raise"
        raise RuntimeError(f"fake tool failure: {reason}")


class DeploymentReadTool(Tool):
    """Stub deployment.read returning a fixed fixture payload."""

    name = "deployment.read"
    destructive = False
    required_permissions = ["deployment:read"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "deployment_id": {"type": "string", "minLength": 1},
        },
        "required": ["deployment_id"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "deployment_id": {"type": "string"},
            "status": {"type": "string"},
            "ready": {"type": "boolean"},
        },
        "required": ["deployment_id", "status", "ready"],
    }

    async def execute(self, args: dict[str, Any]) -> ToolResult:
        dep_id = str(args["deployment_id"])
        return ToolResult(
            output={
                "deployment_id": dep_id,
                "status": "running",
                "ready": True,
            }
        )


def build_fake_tools() -> list[Tool]:
    """Return the fake tool set registered when FORGE_AGENTS_TOOLS_MODE=fake."""
    return [EchoPingTool(), FailRaiseTool(), DeploymentReadTool()]
