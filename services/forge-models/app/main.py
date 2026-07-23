"""forge-models FastAPI application entrypoint."""

from __future__ import annotations

import logging
import time
from contextlib import asynccontextmanager
from typing import AsyncIterator

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, Response

from app import __version__
from app.api.classify import router as classify_router
from app.api.embed import router as embed_router
from app.api.generate import router as generate_router
from app.api.jobs import router as jobs_router
from app.api.models import router as models_router
from app.api.summarize import router as summarize_router
from app.api.usage import router as usage_router
from app.config import Settings, clear_settings_cache, get_settings
from app.health import router as health_router
from app.jobs.store import JobStore
from app.jobs.worker import JobWorker
from app.logging import RequestIdMiddleware, configure_logging
from app.metrics import MetricsMiddleware, UsageMetrics
from app.registry import RegistryLoadError, load_registry

logger = logging.getLogger("forge-models")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    settings: Settings = app.state.settings
    app.state.started_at = time.time()
    app.state.ready = True
    metrics = app.state.registry.metrics
    worker: JobWorker = app.state.job_worker
    await worker.start()
    otel = settings.forge_otel_exporter_otlp_endpoint
    logger.info(
        "starting forge-models",
        extra={
            "backend": settings.forge_models_backend,
            "models_registry_size": metrics.models_registry_size,
            "max_concurrent_jobs": settings.forge_models_max_concurrent_jobs,
            "metrics_enabled": settings.forge_models_metrics_enabled,
            "otel_endpoint": otel or None,
        },
    )
    if otel:
        # Optional OTEL push target is accepted for Observe wiring; Prometheus
        # scrape of GET /metrics is the primary export path in 14.06.
        logger.info("otel exporter endpoint configured (prometheus scrape is primary)")
    try:
        yield
    finally:
        app.state.ready = False
        await worker.stop()
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
    application.state.usage_metrics = UsageMetrics(enabled=resolved.forge_models_metrics_enabled)
    application.state.job_store = JobStore(ttl_seconds=resolved.forge_models_job_ttl_seconds)
    application.state.job_worker = JobWorker(
        application.state.job_store,
        registry,
        resolved,
        usage_metrics=application.state.usage_metrics,
    )
    application.state.ready = False
    application.state.started_at = time.time()

    # Middleware order: last added runs first. Request-id outermost, then metrics.
    application.add_middleware(MetricsMiddleware, metrics=application.state.usage_metrics)
    application.add_middleware(RequestIdMiddleware, logger=logger)
    application.include_router(health_router)
    application.include_router(models_router)
    application.include_router(embed_router)
    application.include_router(generate_router)
    application.include_router(classify_router)
    application.include_router(summarize_router)
    application.include_router(jobs_router)
    application.include_router(usage_router)

    @application.get("/metrics")
    async def prometheus_metrics(request: Request) -> Response:
        usage: UsageMetrics = request.app.state.usage_metrics
        body, content_type = usage.export_prometheus()
        return Response(content=body, media_type=content_type)

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
