"""Events-backed tools: events.publish."""

from __future__ import annotations

from typing import Any

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError
from app.tools.fixtures import fixture_for
from app.tools.http_backend import HttpBackend


class EventsPublishTool(Tool):
    """Publish an event via Forge Events."""

    name = "events.publish"
    destructive = False
    required_permissions = ["events:publish"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "subject": {"type": "string", "minLength": 1},
            "data": {"type": "object"},
            "source": {"type": "string"},
            "idempotency_key": {"type": "string"},
        },
        "required": ["subject", "data"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "event_id": {"type": "string"},
            "stream": {"type": "string"},
            "seq": {"type": "integer"},
            "subject": {"type": "string"},
        },
        "required": ["event_id", "subject"],
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
        subject = str(args["subject"])
        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["subject"] = subject
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "events.publish live backend not configured")

        body: dict[str, Any] = {
            "subject": subject,
            "data": args.get("data") if isinstance(args.get("data"), dict) else {},
        }
        if args.get("source"):
            body["source"] = str(args["source"])
        headers: dict[str, str] = {}
        if args.get("idempotency_key"):
            headers["Idempotency-Key"] = str(args["idempotency_key"])

        resp = await self._backend.request(
            "POST",
            "/v1/events",
            tool=self.name,
            json_body=body,
            headers=headers or None,
        )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"events.publish failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        data = resp.json()
        return ToolResult(
            output={
                "event_id": str(data.get("event_id") or ""),
                "stream": str(data.get("stream") or ""),
                "seq": int(data.get("seq") or 0),
                "subject": subject,
            }
        )
