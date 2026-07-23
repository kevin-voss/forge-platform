"""Name → Tool registry and mode-based builder."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import Literal

from app.tools.base import Tool, schema_is_valid
from app.tools.fake import build_fake_tools

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


def build_tool_registry(mode: ToolsMode = "fake") -> ToolRegistry:
    """Build the tool registry for the configured tools mode.

    `fake` (default/CI): deterministic echo/fail/deployment.read stubs.
    `live`: same stubs until 15.05 wires real platform adapters; logged as such.
    """
    tools_list = build_fake_tools()
    if mode == "live":
        logger.info(
            "tools mode live: using stub tools until platform adapters (15.05)",
            extra={"tools_mode": mode},
        )

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
        },
    )
    return registry
