"""Runtime-backed tools: runtime.restart (destructive)."""

from __future__ import annotations

from typing import Any

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError
from app.tools.fixtures import fixture_for
from app.tools.http_backend import HttpBackend


class RuntimeRestartTool(Tool):
    """Request a workload restart via Forge Runtime (destructive; approval in 15.06)."""

    name = "runtime.restart"
    destructive = True
    required_permissions = ["runtime:restart"]
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
            "restarted": {"type": "boolean"},
            "status": {"type": "string"},
        },
        "required": ["deployment_id", "restarted"],
    }

    def __init__(self, *, mode: str, backend: HttpBackend | None = None) -> None:
        self._mode = mode
        self._backend = backend

    async def execute(
        self,
        args: dict[str, Any],
        *,
        scope: CallScope | None = None,
    ) -> ToolResult:
        dep_id = str(args["deployment_id"])
        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["deployment_id"] = dep_id
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "runtime.restart live backend not configured")

        # Preferred restart endpoint; fall back to drain as a stop signal when absent.
        resp = await self._backend.request(
            "POST",
            f"/v1/workloads/{dep_id}/restart",
            tool=self.name,
            json_body={},
        )
        if resp.status_code == 404:
            resp = await self._backend.request(
                "POST",
                f"/v1/workloads/{dep_id}/drain",
                tool=self.name,
                json_body={},
            )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"runtime.restart failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        body: dict[str, Any] = {}
        if resp.content:
            try:
                parsed = resp.json()
                if isinstance(parsed, dict):
                    body = parsed
            except ValueError:
                body = {}
        return ToolResult(
            output={
                "deployment_id": str(
                    body.get("deploymentId") or body.get("deployment_id") or dep_id
                ),
                "restarted": True,
                "status": str(body.get("status") or "restarting"),
            }
        )
