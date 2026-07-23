"""Models-backed tools: models.generate / models.embed."""

from __future__ import annotations

from typing import Any

from app.permissions import CallScope
from app.tools.base import Tool, ToolResult
from app.tools.errors import ERROR_TOOL_ERROR, ToolError
from app.tools.fixtures import fixture_for
from app.tools.http_backend import HttpBackend


class ModelsGenerateTool(Tool):
    """Generate text via Forge Models."""

    name = "models.generate"
    destructive = False
    required_permissions = ["models:generate"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "model": {"type": "string", "minLength": 1},
            "prompt": {"type": "string", "minLength": 1},
            "max_tokens": {"type": "integer", "minimum": 1, "maximum": 2048},
            "temperature": {"type": "number", "minimum": 0, "maximum": 2},
        },
        "required": ["model", "prompt"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "model": {"type": "string"},
            "text": {"type": "string"},
            "usage": {"type": "object", "additionalProperties": True},
        },
        "required": ["model", "text"],
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
        model = str(args["model"])
        prompt = str(args["prompt"])
        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["model"] = model
            payload["text"] = f"fake:{prompt[:120]}"
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "models.generate live backend not configured")

        body: dict[str, Any] = {
            "prompt": prompt,
            "max_tokens": int(args.get("max_tokens") or 256),
            "temperature": float(
                args.get("temperature") if args.get("temperature") is not None else 0
            ),
        }
        resp = await self._backend.request(
            "POST",
            f"/v1/models/{model}/generate",
            tool=self.name,
            json_body=body,
        )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"models.generate failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        data = resp.json()
        return ToolResult(
            output={
                "model": str(data.get("model") or model),
                "text": str(data.get("text") or ""),
                "usage": data.get("usage") if isinstance(data.get("usage"), dict) else {},
            }
        )


class ModelsEmbedTool(Tool):
    """Embed text via Forge Models."""

    name = "models.embed"
    destructive = False
    required_permissions = ["models:embed"]
    input_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "model": {"type": "string", "minLength": 1},
            "input": {
                "oneOf": [
                    {"type": "string", "minLength": 1},
                    {
                        "type": "array",
                        "minItems": 1,
                        "items": {"type": "string", "minLength": 1},
                    },
                ]
            },
        },
        "required": ["model", "input"],
    }
    output_schema: dict[str, Any] = {
        "type": "object",
        "additionalProperties": False,
        "properties": {
            "model": {"type": "string"},
            "dim": {"type": "integer"},
            "embeddings": {
                "type": "array",
                "items": {"type": "array", "items": {"type": "number"}},
            },
            "usage": {"type": "object", "additionalProperties": True},
        },
        "required": ["model", "dim", "embeddings"],
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
        model = str(args["model"])
        if self._mode == "fake":
            payload = fixture_for(self.name)
            payload["model"] = model
            return ToolResult(output=payload)

        if self._backend is None:
            raise ToolError(ERROR_TOOL_ERROR, "models.embed live backend not configured")

        resp = await self._backend.request(
            "POST",
            f"/v1/models/{model}/embed",
            tool=self.name,
            json_body={"input": args["input"]},
        )
        if resp.status_code >= 400:
            raise ToolError(
                ERROR_TOOL_ERROR,
                f"models.embed failed: HTTP {resp.status_code}: {resp.text[:200]}",
            )
        data = resp.json()
        embeddings = data.get("embeddings") or []
        return ToolResult(
            output={
                "model": str(data.get("model") or model),
                "dim": int(data.get("dim") or (len(embeddings[0]) if embeddings else 0)),
                "embeddings": embeddings,
                "usage": data.get("usage") if isinstance(data.get("usage"), dict) else {},
            }
        )
