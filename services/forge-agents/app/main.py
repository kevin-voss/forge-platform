"""forge-agents FastAPI application entrypoint."""

from __future__ import annotations

import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncIterator

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app import __version__
from app.config import Settings, clear_settings_cache, get_settings
from app.health import router as health_router
from app.logging import RequestIdMiddleware, configure_logging

logger = logging.getLogger("forge-agents")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    settings: Settings = app.state.settings
    app.state.started_at = time.time()
    app.state.ready = True
    logger.info(
        "starting forge-agents",
        extra={"models_url": settings.forge_models_url},
    )
    try:
        yield
    finally:
        app.state.ready = False
        logger.info("shutdown complete")


def create_app(settings: Settings | None = None) -> FastAPI:
    """Build the FastAPI app. Validates settings before returning."""
    clear_settings_cache()
    resolved = settings if settings is not None else get_settings()
    configure_logging(resolved.forge_service_name, resolved.forge_log_level)

    application = FastAPI(
        title="Forge Agents",
        version=resolved.forge_service_version or __version__,
        lifespan=lifespan,
    )
    application.state.settings = resolved
    application.state.ready = False
    application.state.started_at = time.time()

    application.add_middleware(RequestIdMiddleware, logger=logger)
    application.include_router(health_router)

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

    # Validate settings before binding so invalid PORT / URL exit non-zero.
    settings = get_settings()
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
