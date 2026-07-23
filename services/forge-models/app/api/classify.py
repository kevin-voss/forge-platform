"""POST /v1/models/{model}/classify — local zero-shot classification."""

from __future__ import annotations

import logging
import time
from typing import Any

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from app.adapters.base import Capability
from app.api.generate import resolve_gen_adapter
from app.config import Settings
from app.registry import ModelRegistry

logger = logging.getLogger("forge-models")

router = APIRouter(tags=["classification"])


class ClassifyRequest(BaseModel):
    input: str = Field(..., description="Text to classify")
    labels: list[str] = Field(..., description="Candidate labels to score")


def _registry(request: Request) -> ModelRegistry:
    return request.app.state.registry


def _settings(request: Request) -> Settings:
    return request.app.state.settings


def _error(status: int, *, code: str, error: str) -> JSONResponse:
    return JSONResponse(status_code=status, content={"error": error, "code": code})


@router.post("/v1/models/{model_id}/classify")
async def classify_model(model_id: str, body: ClassifyRequest, request: Request) -> JSONResponse:
    settings = _settings(request)
    adapter, err = resolve_gen_adapter(request, model_id, capability=Capability.CLASSIFY)
    if err is not None:
        return err
    assert adapter is not None

    if not isinstance(body.input, str) or body.input == "":
        return _error(422, code="invalid_params", error="input must be a non-empty string")

    if not isinstance(body.labels, list):
        return _error(422, code="invalid_params", error="labels must be a list of strings")
    if len(body.labels) == 0:
        return _error(422, code="invalid_params", error="labels must be a non-empty list")

    max_labels = settings.forge_models_classify_max_labels
    if len(body.labels) > max_labels:
        return _error(
            422,
            code="invalid_params",
            error=f"labels size {len(body.labels)} exceeds max {max_labels}",
        )

    for index, label in enumerate(body.labels):
        if not isinstance(label, str) or label == "":
            return _error(
                422,
                code="invalid_params",
                error=f"labels[{index}] must be a non-empty string",
            )

    started = time.perf_counter()
    scored = adapter.classify(body.input, body.labels)
    latency_seconds = time.perf_counter() - started

    metrics = _registry(request).metrics
    metrics.record_classify(latency_seconds)

    logger.info(
        "classify completed",
        extra={
            "model": model_id,
            "label_count": len(body.labels),
            "latency_ms": round(latency_seconds * 1000.0, 3),
        },
    )

    payload: dict[str, Any] = {"labels": [item.as_dict() for item in scored]}
    return JSONResponse(status_code=200, content=payload)
