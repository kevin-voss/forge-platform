"""Control-backed tools: deployment.read."""

from __future__ import annotations

from typing import Any

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError
from app.tools.fixtures import fixture_for
from app.tools.http_backend import HttpBackend


class DeploymentReadTool(Tool):
    """Read deployment desired/actual status from Forge Control."""

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
            "image": {"type": "string"},
            "desired_replicas": {"type": "integer"},
        },
        "required": ["deployment_id", "status", "ready"],
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
            raise ToolError(ERROR_TOOL_ERROR, "deployment.read live backend not configured")

        resp = await self._backend.request(
            "GET",
            f"/v1/deployments/{dep_id}",
            tool=self.name,
        )
        if resp.status_code == 404:
            raise ToolError(ERROR_TOOL_ERROR, f"deployment not found: {dep_id}")
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"deployment.read failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        body = resp.json()
        status = str(body.get("status") or "unknown")
        ready = status.lower() in {"deployed", "ready", "running"}
        # Prefer reconcile when available; ignore soft failures.
        try:
            recon = await self._backend.request(
                "GET",
                f"/v1/deployments/{dep_id}/reconcile",
                tool=self.name,
            )
            if recon.status_code < 400:
                recon_body = recon.json()
                if recon_body.get("status"):
                    status = str(recon_body["status"])
                replicas = recon_body.get("actual", {}).get("replicas") or []
                if isinstance(replicas, list) and replicas:
                    ready = all(
                        str(r.get("status", "")).lower() == "ready"
                        for r in replicas
                        if isinstance(r, dict)
                    )
        except ToolError:
            pass

        return ToolResult(
            output={
                "deployment_id": str(body.get("id") or dep_id),
                "status": status,
                "ready": ready,
                "image": str(body.get("image") or ""),
                "desired_replicas": int(body.get("desiredReplicas") or 0),
            }
        )
