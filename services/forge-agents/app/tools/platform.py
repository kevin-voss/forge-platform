"""Assemble platform tools (Control/Runtime/Observe/Storage/Models/Events/Memory)."""

from __future__ import annotations

from app.tools.backend_config import ToolBackendConfig
from app.tools.base import Tool
from app.tools.control import DeploymentReadTool
from app.tools.events import EventsPublishTool
from app.tools.http_backend import HttpBackend
from app.tools.memory import MemorySearchTool, MemoryUpsertTool
from app.tools.models import ModelsEmbedTool, ModelsGenerateTool
from app.tools.observe import LogsSearchTool, MetricsQueryTool
from app.tools.runtime import RuntimeRestartTool
from app.tools.storage import StorageGetTool, StoragePutTool


def build_platform_tools(config: ToolBackendConfig) -> list[Tool]:
    """Build platform tools for the configured fake|live mode."""
    mode = config.mode
    timeout = config.timeout_seconds

    control_backend: HttpBackend | None = None
    runtime_backend: HttpBackend | None = None
    observe_backend: HttpBackend | None = None
    storage_backend: HttpBackend | None = None
    models_backend: HttpBackend | None = None
    events_backend: HttpBackend | None = None
    memory_backend: HttpBackend | None = None

    if mode == "live":
        control_backend = HttpBackend(
            config.control_url, timeout_seconds=timeout, service="control"
        )
        runtime_backend = HttpBackend(
            config.runtime_url, timeout_seconds=timeout, service="runtime"
        )
        observe_backend = HttpBackend(
            config.observe_url, timeout_seconds=timeout, service="observe"
        )
        storage_backend = HttpBackend(
            config.storage_url, timeout_seconds=timeout, service="storage"
        )
        models_backend = HttpBackend(config.models_url, timeout_seconds=timeout, service="models")
        events_backend = HttpBackend(config.events_url, timeout_seconds=timeout, service="events")
        memory_backend = HttpBackend(config.memory_url, timeout_seconds=timeout, service="memory")

    return [
        DeploymentReadTool(mode=mode, backend=control_backend),
        LogsSearchTool(mode=mode, backend=observe_backend),
        MetricsQueryTool(mode=mode, backend=observe_backend),
        RuntimeRestartTool(mode=mode, backend=runtime_backend),
        StorageGetTool(mode=mode, backend=storage_backend),
        StoragePutTool(mode=mode, backend=storage_backend),
        ModelsGenerateTool(mode=mode, backend=models_backend),
        ModelsEmbedTool(mode=mode, backend=models_backend),
        EventsPublishTool(mode=mode, backend=events_backend),
        MemorySearchTool(mode=mode, backend=memory_backend),
        MemoryUpsertTool(mode=mode, backend=memory_backend),
    ]
