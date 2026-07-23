"""Observe-backed tools: logs.search, metrics.query."""

from __future__ import annotations

from typing import Any

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError
from app.tools.fixtures import fixture_for
from app.tools.http_backend import HttpBackend


class LogsSearchTool(Tool):
    """Query correlated logs via Forge Observe."""

    name = "logs.search"
    destructive = False
    required_permissions = ["logs:read"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "project": {"type": "string"},
            "deployment": {"type": "string"},
            "service": {"type": "string"},
            "request_id": {"type": "string"},
            "trace_id": {"type": "string"},
            "q": {"type": "string"},
            "limit": {"type": "integer", "minimum": 1, "maximum": 500},
        },
        "required": [],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "entries": {
                "type": "array",
                "items": {
                    "type": "object",
                    "additionalProperties": True,
                },
            },
            "next_cursor": {"type": "string"},
        },
        "required": ["entries"],
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
        if self._mode == "fake":
            return ToolResult(output=fixture_for(self.name))

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "logs.search live backend not configured")

        params: dict[str, Any] = {}
        for key in ("project", "deployment", "service", "request_id", "trace_id", "q", "limit"):
            if key in args and args[key] is not None and args[key] != "":
                params[key] = args[key]
        if "project" not in params and scope is not None and scope.project_id:
            params["project"] = scope.project_id
        if not any(
            params.get(k) for k in ("project", "deployment", "service", "request_id", "trace_id")
        ):
            raise ToolError(
                ERROR_TOOL_ERROR,
                "logs.search requires project, deployment, service, request_id, or trace_id",
            )

        resp = await self._backend.request(
            "GET",
            "/v1/logs",
            tool=self.name,
            params=params,
        )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"logs.search failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        body = resp.json()
        return ToolResult(
            output={
                "entries": list(body.get("entries") or []),
                "next_cursor": str(body.get("next_cursor") or ""),
            }
        )


class MetricsQueryTool(Tool):
    """Query metrics via Forge Observe (Prometheus-compatible query API)."""

    name = "metrics.query"
    destructive = False
    required_permissions = ["metrics:read"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "query": {"type": "string", "minLength": 1},
            "time": {"type": "string"},
        },
        "required": ["query"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "query": {"type": "string"},
            "result_type": {"type": "string"},
            "samples": {
                "type": "array",
                "items": {
                    "type": "object",
                    "additionalProperties": True,
                },
            },
        },
        "required": ["query", "result_type", "samples"],
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
        query = str(args["query"])
        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["query"] = query
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "metrics.query live backend not configured")

        params: dict[str, Any] = {"query": query}
        if args.get("time"):
            params["time"] = args["time"]

        # Prefer Observe metrics facade; fall back to Prometheus instant-query path.
        resp = await self._backend.request(
            "GET",
            "/v1/metrics/query",
            tool=self.name,
            params=params,
        )
        if resp.status_code == 404:
            resp = await self._backend.request(
                "GET",
                "/api/v1/query",
                tool=self.name,
                params=params,
            )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"metrics.query failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        body = resp.json()
        # Normalize Observe or Prometheus response shapes.
        if "samples" in body:
            return ToolResult(
                output={
                    "query": query,
                    "result_type": str(body.get("result_type") or "vector"),
                    "samples": list(body.get("samples") or []),
                }
            )
        data = body.get("data") if isinstance(body.get("data"), dict) else {}
        result_type = str(data.get("resultType") or "vector")
        samples: list[dict[str, Any]] = []
        for item in data.get("result") or []:
            if not isinstance(item, dict):
                continue
            value = item.get("value")
            sample: dict[str, Any] = {"metric": item.get("metric") or {}}
            if isinstance(value, list) and len(value) >= 2:
                sample["timestamp"] = value[0]
                try:
                    sample["value"] = float(value[1])
                except (TypeError, ValueError):
                    sample["value"] = value[1]
            samples.append(sample)
        return ToolResult(
            output={
                "query": query,
                "result_type": result_type,
                "samples": samples,
            }
        )
