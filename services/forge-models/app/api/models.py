"""Model registry read APIs: list, get, and per-model health."""

from __future__ import annotations

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse

from app.registry import ModelRegistry, serialize_model

router = APIRouter(tags=["models"])


def _registry(request: Request) -> ModelRegistry:
    return request.app.state.registry


@router.get("/v1/models")
async def list_models(request: Request) -> dict:
    registry = _registry(request)
    registry.refresh_metrics()
    return {"models": [serialize_model(adapter) for adapter in registry.list()]}


@router.get("/v1/models/{model_id}/health")
async def model_health(model_id: str, request: Request) -> JSONResponse:
    # Declared before get_model so `/health` is not captured as a model id suffix path.
    registry = _registry(request)
    adapter = registry.get(model_id)
    if adapter is None:
        return JSONResponse(
            status_code=404,
            content={"error": f"model not found: {model_id}", "code": "model_not_found"},
        )
    status = adapter.health()
    registry.refresh_metrics()
    return JSONResponse(status_code=200, content={"status": status.value})


@router.get("/v1/models/{model_id}")
async def get_model(model_id: str, request: Request) -> JSONResponse:
    adapter = _registry(request).get(model_id)
    if adapter is None:
        return JSONResponse(
            status_code=404,
            content={"error": f"model not found: {model_id}", "code": "model_not_found"},
        )
    return JSONResponse(status_code=200, content=serialize_model(adapter))
