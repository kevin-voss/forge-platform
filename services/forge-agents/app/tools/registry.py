"""Name → Tool registry and mode-based builder."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import Literal

from app.tools.backend_config import ToolBackendConfig
from app.tools.base import Tool, schema_is_valid
from app.tools.fake import build_fake_tools
from app.tools.platform import build_platform_tools

logger = logging.getLogger("forge-agents")

ToolsMode = Literal["fake", "live"]


@dataclass
class ToolRegistry:
    """In-memory map of tool name → Tool instance."""

    tools: dict[str, Tool]
    tools_registry_size: int = field(init=False)

    def __post_init__(self) -> None:
        self.tools_registry_size = len(self.tools)

    def get(self, name: str) -> Tool | None:
        return self.tools.get(name)

    def list(self) -> list[Tool]:
        return [self.tools[key] for key in sorted(self.tools)]

    def to_api_list(self) -> list[dict[str, object]]:
        return [
            {
                "name": tool.name,
                "input_schema": tool.input_schema,
                "output_schema": tool.output_schema,
                "destructive": tool.destructive,
                "required_permissions": list(tool.required_permissions),
            }
            for tool in self.list()
        ]


def build_tool_registry(
    mode: ToolsMode = "fake",
    *,
    config: ToolBackendConfig | None = None,
) -> ToolRegistry:
    """Build the tool registry for the configured tools mode.

    Always includes CI helpers (`echo.ping`, `fail.raise`) plus platform tools
    whose backends are fake fixtures or live HTTP clients.
    """
    resolved = config or ToolBackendConfig(mode=mode)
    if resolved.mode != mode:
        resolved = ToolBackendConfig(
            mode=mode,
            control_url=resolved.control_url,
            runtime_url=resolved.runtime_url,
            observe_url=resolved.observe_url,
            storage_url=resolved.storage_url,
            models_url=resolved.models_url,
            events_url=resolved.events_url,
            timeout_seconds=resolved.timeout_seconds,
        )

    tools_list = [*build_fake_tools(), *build_platform_tools(resolved)]
    by_name: dict[str, Tool] = {}
    for tool in tools_list:
        if tool.name in by_name:
            raise ValueError(f"duplicate tool name '{tool.name}'")
        if not schema_is_valid(tool.input_schema):
            raise ValueError(f"tool '{tool.name}' has invalid input_schema")
        if not schema_is_valid(tool.output_schema):
            raise ValueError(f"tool '{tool.name}' has invalid output_schema")
        by_name[tool.name] = tool

    registry = ToolRegistry(tools=by_name)
    logger.info(
        "tools registry loaded",
        extra={
            "tools_registry_size": registry.tools_registry_size,
            "tool_names": sorted(by_name),
            "tools_mode": mode,
            "destructive_tools": sorted(t.name for t in by_name.values() if t.destructive),
        },
    )
    return registry
