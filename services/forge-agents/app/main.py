"""forge-agents FastAPI application entrypoint."""

from __future__ import annotations

import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncIterator

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app import __version__
from app.agents.loader import AgentLoadError, load_registry
from app.api.agents import router as agents_router
from app.api.runs import router as runs_router
from app.api.tools import router as tools_router
from app.config import Settings, clear_settings_cache, get_settings
from app.health import router as health_router
from app.logging import RequestIdMiddleware, configure_logging
from app.permissions import PermissionChecker
from app.run.engine import RunEngine
from app.run.metrics import RunMetrics
from app.run.model_client import FakeModelClient, HttpModelClient
from app.run.store import RunStore
from app.tools.backend_config import ToolBackendConfig
from app.tools.invoker import ToolInvoker
from app.tools.metrics import ToolMetrics
from app.tools.registry import ToolsMode, build_tool_registry

logger = logging.getLogger("forge-agents")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    settings: Settings = app.state.settings
    registry = app.state.registry
    tool_registry = app.state.tool_registry
    engine: RunEngine = app.state.run_engine
    app.state.started_at = time.time()
    app.state.ready = True
    logger.info(
        "starting forge-agents",
        extra={
            "models_url": settings.forge_models_url,
            "agents_registry_size": registry.agents_registry_size,
            "agent_names": [a.name for a in registry.list()],
            "tools_registry_size": tool_registry.tools_registry_size,
            "tool_names": [t.name for t in tool_registry.list()],
            "tools_mode": settings.forge_agents_tools_mode,
            "tool_timeout_seconds": settings.forge_agents_tool_timeout_seconds,
            "db_path": settings.forge_agents_db_path,
            "max_concurrent_runs": settings.forge_agents_max_concurrent_runs,
        },
    )
    try:
        yield
    finally:
        app.state.ready = False
        await engine.aclose()
        store: RunStore = app.state.run_store
        store.close()
        logger.info("shutdown complete")


def create_app(settings: Settings | None = None) -> FastAPI:
    """Build the FastAPI app. Validates settings and registries before returning."""
    clear_settings_cache()
    resolved = settings if settings is not None else get_settings()
    configure_logging(resolved.forge_service_name, resolved.forge_log_level)

    try:
        registry = load_registry(resolved.forge_agents_defs_dir or None)
    except AgentLoadError:
        # Re-raise with original message for clear startup failure.
        raise

    mode: ToolsMode = resolved.forge_agents_tools_mode  # type: ignore[assignment]
    tool_backend = ToolBackendConfig(
        mode=mode,
        control_url=resolved.forge_control_url,
        runtime_url=resolved.forge_runtime_url,
        observe_url=resolved.forge_observe_url,
        storage_url=resolved.forge_storage_url,
        models_url=resolved.forge_models_url,
        events_url=resolved.forge_events_url,
        timeout_seconds=resolved.forge_agents_tool_timeout_seconds,
    )
    tool_registry = build_tool_registry(mode, config=tool_backend)
    tool_metrics = ToolMetrics()
    tool_invoker = ToolInvoker(
        tool_registry,
        checker=PermissionChecker(),
        metrics=tool_metrics,
    )
    run_store = RunStore(resolved.forge_agents_db_path)
    run_metrics = RunMetrics()
    http_model = HttpModelClient(resolved.forge_models_url)
    fake_model = FakeModelClient()
    run_engine = RunEngine(
        store=run_store,
        registry=registry,
        invoker=tool_invoker,
        model_client=http_model,
        fake_model_client=fake_model,
        max_concurrent_runs=resolved.forge_agents_max_concurrent_runs,
        metrics=run_metrics,
    )

    application = FastAPI(
        title="Forge Agents",
        version=resolved.forge_service_version or __version__,
        lifespan=lifespan,
    )
    application.state.settings = resolved
    application.state.registry = registry
    application.state.tool_registry = tool_registry
    application.state.tool_invoker = tool_invoker
    application.state.tool_metrics = tool_metrics
    application.state.run_store = run_store
    application.state.run_engine = run_engine
    application.state.run_metrics = run_metrics
    application.state.ready = False
    application.state.started_at = time.time()

    application.add_middleware(RequestIdMiddleware, logger=logger)
    application.include_router(health_router)
    application.include_router(agents_router)
    application.include_router(tools_router)
    application.include_router(runs_router)

    @application.get("/")
    async def identity(request: Request) -> JSONResponse:
        cfg: Settings = request.app.state.settings
        started = float(getattr(request.app.state, "started_at", time.time()))
        return JSONResponse(
            {
                "service": cfg.forge_service_name,
                "language": "python",
                "status": "running",
                "version": cfg.forge_service_version,
                "uptime_seconds": max(0.0, time.time() - started),
            }
        )

    return application


def main() -> None:
    """Run uvicorn with configured PORT (local `make dev`)."""
    import uvicorn

    # Validate settings + registries before binding so bad config exits non-zero.
    settings = get_settings()
    try:
        load_registry(settings.forge_agents_defs_dir or None)
    except AgentLoadError as exc:
        logger.error("agent registry load failed: %s", exc)
        raise SystemExit(1) from exc
    try:
        build_tool_registry(settings.forge_agents_tools_mode)  # type: ignore[arg-type]
    except ValueError as exc:
        logger.error("tool registry load failed: %s", exc)
        raise SystemExit(1) from exc

    uvicorn.run(
        "app.main:create_app",
        factory=True,
        host="0.0.0.0",
        port=settings.port,
        log_config=None,
        access_log=False,
        timeout_graceful_shutdown=settings.forge_shutdown_grace_seconds,
    )


if __name__ == "__main__":
    main()
