"""Deterministic CI-only tools (echo / fail) used by fixture agents."""

from __future__ import annotations

from typing import Any

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError


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

    async def execute(
        self,
        args: dict[str, Any],
        *,
        scope: CallScope | None = None,
    ) -> ToolResult:
        return ToolResult(output={"echo": str(args["message"])})


class FailRaiseTool(Tool):
    """Always fails at execute time with a normalized ToolError."""

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

    async def execute(
        self,
        args: dict[str, Any],
        *,
        scope: CallScope | None = None,
    ) -> ToolResult:
        reason = args.get("reason") or "fail.raise"
        raise ToolError(ERROR_TOOL_ERROR, f"fake tool failure: {reason}")


def build_fake_tools() -> list[Tool]:
    """Return CI helper tools that are always registered."""
    return [EchoPingTool(), FailRaiseTool()]
