"""Memory-backed tools: memory.search / memory.upsert."""

from __future__ import annotations

from collections import defaultdict
from threading import Lock
from typing import Any
from urllib.parse import quote

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError
from app.tools.fixtures import fixture_for
from app.tools.http_backend import HttpBackend

_memory_tool_calls: dict[str, int] = defaultdict(int)
_memory_lock = Lock()


def record_memory_tool_call(op: str) -> None:
    """Increment agent_memory_tool_calls_total{op}."""
    with _memory_lock:
        _memory_tool_calls[op] += 1


def memory_tool_calls_snapshot() -> list[dict[str, object]]:
    with _memory_lock:
        return [
            {"op": op, "value": value}
            for op, value in sorted(_memory_tool_calls.items())
        ]


def reset_memory_tool_calls() -> None:
    with _memory_lock:
        _memory_tool_calls.clear()


class MemorySearchTool(Tool):
    """Semantic search over a Forge Memory collection (read)."""

    name = "memory.search"
    destructive = False
    required_permissions = ["memory:read"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "collection": {"type": "string", "minLength": 1},
            "query": {"type": "string", "minLength": 1},
            "top_k": {"type": "integer", "minimum": 1, "maximum": 100},
            "project_id": {"type": "string"},
            "model": {"type": "string"},
        },
        "required": ["collection", "query"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "results": {
                "type": "array",
                "items": {
                    "type": "object",
                    "additionalProperties": False,
                    "properties": {
                        "id": {"type": "string"},
                        "score": {"type": "number"},
                        "metadata": {"type": "object", "additionalProperties": True},
                    },
                    "required": ["id", "score", "metadata"],
                },
            },
        },
        "required": ["results"],
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
        collection = str(args["collection"])
        query = str(args["query"])
        top_k = int(args.get("top_k") or 5)
        record_memory_tool_call("search")

        if self._mode == "fake":
            payload = fixture_for(self.name)
            # Allow tests/demos to inject the query into metadata for citation.
            results = list(payload.get("results") or [])
            if results and isinstance(results[0], dict):
                first = dict(results[0])
                meta = dict(first.get("metadata") or {})
                meta.setdefault("query", query)
                first["metadata"] = meta
                results[0] = first
            return ToolResult(output={"results": results})

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "memory.search live backend not configured")

        project = str(args.get("project_id") or (scope.project_id if scope else "") or "")
        if not project:
            raise ToolError(ERROR_TOOL_ERROR, "memory.search requires project_id")

        body: dict[str, Any] = {"text": query, "top_k": top_k}
        if args.get("model"):
            body["model"] = str(args["model"])

        resp = await self._backend.request(
            "POST",
            f"/v1/collections/{quote(collection, safe='')}/query",
            tool=self.name,
            json_body=body,
            headers={"X-Forge-Project": project},
        )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"memory.search failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        data = resp.json() if resp.content else {}
        results = data.get("results") if isinstance(data.get("results"), list) else []
        normalized = []
        for item in results:
            if not isinstance(item, dict):
                continue
            normalized.append(
                {
                    "id": str(item.get("id") or ""),
                    "score": float(item.get("score") or 0.0),
                    "metadata": item.get("metadata")
                    if isinstance(item.get("metadata"), dict)
                    else {},
                }
            )
        return ToolResult(output={"results": normalized})


class MemoryUpsertTool(Tool):
    """Upsert text items into a Forge Memory collection (write, non-destructive)."""

    name = "memory.upsert"
    destructive = False
    required_permissions = ["memory:write"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "collection": {"type": "string", "minLength": 1},
            "items": {
                "type": "array",
                "minItems": 1,
                "items": {
                    "type": "object",
                    "additionalProperties": False,
                    "properties": {
                        "id": {"type": "string", "minLength": 1},
                        "text": {"type": "string", "minLength": 1},
                        "metadata": {"type": "object", "additionalProperties": True},
                    },
                    "required": ["id", "text"],
                },
            },
            "project_id": {"type": "string"},
            "model": {"type": "string"},
        },
        "required": ["collection", "items"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "upserted": {"type": "integer", "minimum": 0},
            "collection": {"type": "string"},
        },
        "required": ["upserted", "collection"],
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
        collection = str(args["collection"])
        items = args["items"]
        record_memory_tool_call("upsert")

        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["collection"] = collection
            payload["upserted"] = len(items) if isinstance(items, list) else 0
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "memory.upsert live backend not configured")

        project = str(args.get("project_id") or (scope.project_id if scope else "") or "")
        if not project:
            raise ToolError(ERROR_TOOL_ERROR, "memory.upsert requires project_id")

        body: dict[str, Any] = {"items": items}
        if args.get("model"):
            body["model"] = str(args["model"])

        resp = await self._backend.request(
            "POST",
            f"/v1/collections/{quote(collection, safe='')}/upsert",
            tool=self.name,
            json_body=body,
            headers={"X-Forge-Project": project},
        )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"memory.upsert failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        data = resp.json() if resp.content else {}
        return ToolResult(
            output={
                "collection": collection,
                "upserted": int(data.get("upserted") or 0),
            }
        )
