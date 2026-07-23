"""Storage-backed tools: storage.get / storage.put."""

from __future__ import annotations

import base64
from typing import Any
from urllib.parse import quote

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError
from app.tools.fixtures import fixture_for
from app.tools.http_backend import HttpBackend

_MAX_GET_BYTES = 64 * 1024


def _object_path(bucket: str, key: str) -> str:
    # Preserve nested keys; quote each segment safely.
    quoted_key = "/".join(quote(part, safe="") for part in key.split("/"))
    return f"/v1/buckets/{quote(bucket, safe='')}/objects/{quoted_key}"


class StorageGetTool(Tool):
    """Download a small object (text or base64) from Forge Storage."""

    name = "storage.get"
    destructive = False
    required_permissions = ["storage:read"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "bucket": {"type": "string", "minLength": 1},
            "key": {"type": "string", "minLength": 1},
            "project_id": {"type": "string"},
        },
        "required": ["bucket", "key"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "bucket": {"type": "string"},
            "key": {"type": "string"},
            "content_type": {"type": "string"},
            "sha256": {"type": "string"},
            "size": {"type": "integer"},
            "content": {"type": "string"},
            "content_base64": {"type": "string"},
        },
        "required": ["bucket", "key", "size"],
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
        bucket = str(args["bucket"])
        key = str(args["key"])
        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["bucket"] = bucket
            payload["key"] = key
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "storage.get live backend not configured")

        project = str(args.get("project_id") or (scope.project_id if scope else "") or "")
        if not project:
            raise ToolError(ERROR_TOOL_ERROR, "storage.get requires project_id")

        resp = await self._backend.request(
            "GET",
            _object_path(bucket, key),
            tool=self.name,
            headers={"X-Forge-Project": project},
            expect_json=False,
        )
        if resp.status_code == 404:
            raise ToolError(ERROR_TOOL_ERROR, f"object not found: {bucket}/{key}")
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"storage.get failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        raw = resp.content[:_MAX_GET_BYTES]
        sha = resp.headers.get("X-Content-SHA256") or resp.headers.get("ETag", "").strip('"')
        content_type = resp.headers.get("Content-Type") or "application/octet-stream"
        out: dict[str, Any] = {
            "bucket": bucket,
            "key": key,
            "content_type": content_type,
            "sha256": sha,
            "size": len(raw),
            "content_base64": base64.b64encode(raw).decode("ascii"),
        }
        try:
            out["content"] = raw.decode("utf-8")
        except UnicodeDecodeError:
            out["content"] = ""
        return ToolResult(output=out)


class StoragePutTool(Tool):
    """Upload a small text/base64 object to Forge Storage."""

    name = "storage.put"
    destructive = False
    required_permissions = ["storage:write"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "bucket": {"type": "string", "minLength": 1},
            "key": {"type": "string", "minLength": 1},
            "content": {"type": "string"},
            "content_base64": {"type": "string"},
            "content_type": {"type": "string"},
            "project_id": {"type": "string"},
        },
        "required": ["bucket", "key"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "bucket": {"type": "string"},
            "key": {"type": "string"},
            "sha256": {"type": "string"},
            "size": {"type": "integer"},
            "created": {"type": "boolean"},
        },
        "required": ["bucket", "key", "size", "created"],
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
        bucket = str(args["bucket"])
        key = str(args["key"])
        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["bucket"] = bucket
            payload["key"] = key
            if "content" in args and isinstance(args["content"], str):
                payload["size"] = len(args["content"].encode("utf-8"))
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "storage.put live backend not configured")

        project = str(args.get("project_id") or (scope.project_id if scope else "") or "")
        if not project:
            raise ToolError(ERROR_TOOL_ERROR, "storage.put requires project_id")

        if args.get("content_base64"):
            raw = base64.b64decode(str(args["content_base64"]))
        elif "content" in args and args["content"] is not None:
            raw = str(args["content"]).encode("utf-8")
        else:
            raise ToolError(ERROR_TOOL_ERROR, "storage.put requires content or content_base64")

        content_type = str(args.get("content_type") or "application/octet-stream")
        resp = await self._backend.request(
            "PUT",
            _object_path(bucket, key),
            tool=self.name,
            content=raw,
            headers={
                "X-Forge-Project": project,
                "Content-Type": content_type,
            },
            expect_json=True,
        )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"storage.put failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        body = resp.json() if resp.content else {}
        return ToolResult(
            output={
                "bucket": bucket,
                "key": key,
                "sha256": str(body.get("sha256") or body.get("etag") or ""),
                "size": int(body.get("size") or len(raw)),
                "created": resp.status_code == 201,
            }
        )
