"""forge-models FastAPI application entrypoint."""

from __future__ import annotations

import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncIterator

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app import __version__
from app.api.embed import router as embed_router
from app.api.models import router as models_router
from app.config import Settings, clear_settings_cache, get_settings
from app.health import router as health_router
from app.logging import RequestIdMiddleware, configure_logging
from app.registry import RegistryLoadError, load_registry

logger = logging.getLogger("forge-models")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    settings: Settings = app.state.settings
    app.state.started_at = time.time()
    app.state.ready = True
    metrics = app.state.registry.metrics
    logger.info(
        "starting forge-models",
        extra={
            "backend": settings.forge_models_backend,
            "models_registry_size": metrics.models_registry_size,
        },
    )
    try:
        yield
    finally:
        app.state.ready = False
        logger.info("shutdown complete")


def create_app(settings: Settings | None = None) -> FastAPI:
    """Build the FastAPI app. Validates settings and registry before returning."""
    clear_settings_cache()
    resolved = settings if settings is not None else get_settings()
    configure_logging(resolved.forge_service_name, resolved.forge_log_level)

    try:
        registry = load_registry(
            resolved.forge_models_config or None,
            local_model_path=resolved.forge_models_local_model_path or None,
        )
    except RegistryLoadError:
        # Re-raise with original message for clear startup failure.
        raise

    application = FastAPI(
        title="Forge Models",
        version=resolved.forge_service_version or __version__,
        lifespan=lifespan,
    )
    application.state.settings = resolved
    application.state.registry = registry
    application.state.ready = False
    application.state.started_at = time.time()

    application.add_middleware(RequestIdMiddleware, logger=logger)
    application.include_router(health_router)
    application.include_router(models_router)
    application.include_router(embed_router)

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

    # Validate settings + registry before binding so bad config exits non-zero.
    settings = get_settings()
    try:
        load_registry(
            settings.forge_models_config or None,
            local_model_path=settings.forge_models_local_model_path or None,
        )
    except RegistryLoadError as exc:
        logger.error("registry load failed: %s", exc)
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
