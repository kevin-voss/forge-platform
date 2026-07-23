"""URLs + mode for platform tool backends."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

ToolsMode = Literal["fake", "live"]


@dataclass(frozen=True)
class ToolBackendConfig:
    """Service URLs and timeout used by platform tools."""

    mode: ToolsMode = "fake"
    control_url: str = "http://forge-control:4001"
    runtime_url: str = "http://forge-runtime:4102"
    observe_url: str = "http://forge-observe:4106"
    storage_url: str = "http://forge-storage:4107"
    models_url: str = "http://forge-models:4300"
    events_url: str = "http://forge-events:4105"
    memory_url: str = "http://forge-memory:4303"
    timeout_seconds: float = 15.0
