"""Permission-aware tool invocation pipeline (used by the run engine in 15.04)."""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any, Sequence

from app.agents.models import AgentDefinition
from app.permissions import CallScope, PermissionChecker
from app.tools.base import ToolResult, validate_against_schema
from app.tools.metrics import ToolMetrics, default_tool_metrics
from app.tools.registry import ToolRegistry

logger = logging.getLogger("forge-agents")

# Rejection reasons (OpenAPI / audit contract).
REASON_UNKNOWN_TOOL = "unknown_tool"
REASON_NOT_DECLARED = "not_declared"
REASON_PERMISSION_DENIED = "permission_denied"
REASON_INVALID_ARGUMENTS = "invalid_arguments"

DENY_REASONS = frozenset(
    {
        REASON_UNKNOWN_TOOL,
        REASON_NOT_DECLARED,
        REASON_PERMISSION_DENIED,
        REASON_INVALID_ARGUMENTS,
    }
)


@dataclass(frozen=True)
class InvokeResult:
    """Outcome of a single tool invocation attempt."""

    ok: bool
    tool: str
    decision: str  # allow | deny
    reason: str | None = None
    output: dict[str, Any] | None = None
    error: str | None = None

    def to_audit_dict(self) -> dict[str, object]:
        payload: dict[str, object] = {
            "tool": self.tool,
            "decision": self.decision,
            "ok": self.ok,
        }
        if self.reason is not None:
            payload["reason"] = self.reason
        if self.error is not None:
            payload["error"] = self.error
        return payload


class ToolInvoker:
    """Enforce declared → registered → schema → permission before execute.

    Check order (deny-by-default):
    1. registry membership → unknown_tool (hallucination)
    2. agent.tools membership → not_declared (overreach)
    3. input schema → invalid_arguments
    4. permission scope → permission_denied
    5. tool.execute
    """

    def __init__(
        self,
        registry: ToolRegistry,
        *,
        checker: PermissionChecker | None = None,
        metrics: ToolMetrics | None = None,
    ) -> None:
        self._registry = registry
        self._checker = checker or PermissionChecker()
        self._metrics = metrics or default_tool_metrics

    async def invoke(
        self,
        *,
        agent: AgentDefinition,
        tool_name: str,
        arguments: dict[str, Any] | None,
        scope: CallScope,
    ) -> InvokeResult:
        name = (tool_name or "").strip()
        args = arguments if isinstance(arguments, dict) else {}

        tool = self._registry.get(name)
        if tool is None:
            return self._deny(
                name,
                REASON_UNKNOWN_TOOL,
                error=f"unknown tool: {name}",
                agent=agent.name,
                project_id=scope.project_id,
            )

        if name not in agent.tools:
            return self._deny(
                name,
                REASON_NOT_DECLARED,
                error=f"tool not declared by agent '{agent.name}': {name}",
                agent=agent.name,
                project_id=scope.project_id,
            )

        schema_errors = validate_against_schema(args, tool.input_schema)
        if schema_errors:
            return self._deny(
                name,
                REASON_INVALID_ARGUMENTS,
                error="; ".join(schema_errors),
                agent=agent.name,
                project_id=scope.project_id,
            )

        required: Sequence[str] = tool.required_permissions
        if not self._checker.has_permission(scope, required):
            missing = self._checker.missing_permissions(scope, required)
            return self._deny(
                name,
                REASON_PERMISSION_DENIED,
                error=f"missing permissions: {', '.join(missing)}",
                agent=agent.name,
                project_id=scope.project_id,
                missing_permissions=missing,
            )

        result: ToolResult = await tool.execute(args)
        self._metrics.record_allow(name)
        logger.info(
            "tool call allowed",
            extra={
                "tool": name,
                "decision": "allow",
                "agent": agent.name,
                "project_id": scope.project_id,
            },
        )
        return InvokeResult(
            ok=True,
            tool=name,
            decision="allow",
            output=result.output,
        )

    def _deny(
        self,
        tool: str,
        reason: str,
        *,
        error: str,
        agent: str,
        project_id: str,
        missing_permissions: Sequence[str] | None = None,
    ) -> InvokeResult:
        self._metrics.record_deny(tool or "unknown", reason)
        extra: dict[str, object] = {
            "tool": tool,
            "decision": "deny",
            "reason": reason,
            "agent": agent,
            "project_id": project_id,
        }
        if missing_permissions:
            extra["missing_permissions"] = list(missing_permissions)
        logger.info("tool call denied", extra=extra)
        return InvokeResult(
            ok=False,
            tool=tool,
            decision="deny",
            reason=reason,
            error=error,
        )
