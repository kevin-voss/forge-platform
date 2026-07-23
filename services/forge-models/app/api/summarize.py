"""POST /v1/models/{model}/summarize — thin wrapper over generate."""

from __future__ import annotations

import logging
import time
from typing import Any

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from app.adapters.base import Capability
from app.adapters.local_gen import summarize_prompt
from app.api.generate import resolve_gen_adapter, validate_generate_params
from app.config import Settings
from app.registry import ModelRegistry

logger = logging.getLogger("forge-models")

router = APIRouter(tags=["summarization"])


class SummarizeRequest(BaseModel):
    input: str = Field(..., description="Text to summarize")
    max_tokens: int | None = Field(
        default=None,
        description="Max summary tokens (capped by FORGE_MODELS_GEN_MAX_TOKENS)",
    )
    temperature: float | None = Field(
        default=None,
        description="Sampling temperature; 0 is deterministic",
    )


def _registry(request: Request) -> ModelRegistry:
    return request.app.state.registry


def _settings(request: Request) -> Settings:
    return request.app.state.settings


def _error(status: int, *, code: str, error: str) -> JSONResponse:
    return JSONResponse(status_code=status, content={"error": error, "code": code})


@router.post("/v1/models/{model_id}/summarize")
async def summarize_model(model_id: str, body: SummarizeRequest, request: Request) -> JSONResponse:
    settings = _settings(request)
    adapter, err = resolve_gen_adapter(request, model_id, capability=Capability.SUMMARIZE)
    if err is not None:
        return err
    assert adapter is not None

    if not isinstance(body.input, str) or body.input == "":
        return _error(422, code="invalid_params", error="input must be a non-empty string")

    params = validate_generate_params(
        max_tokens=body.max_tokens,
        temperature=body.temperature,
        settings=settings,
    )
    if isinstance(params, JSONResponse):
        return params
    max_tokens, temperature = params

    prompt = summarize_prompt(body.input)
    started = time.perf_counter()
    result = adapter.generate(prompt, max_tokens=max_tokens, temperature=temperature)
    latency_seconds = time.perf_counter() - started

    metrics = _registry(request).metrics
    metrics.record_summarize(latency_seconds)

    logger.info(
        "summarize completed",
        extra={
            "model": model_id,
            "prompt_tokens": result.usage.prompt_tokens,
            "completion_tokens": result.usage.completion_tokens,
            "total_tokens": result.usage.total_tokens,
            "latency_ms": round(latency_seconds * 1000.0, 3),
        },
    )

    payload: dict[str, Any] = {
        "summary": result.text,
        "usage": result.usage.as_dict(),
    }
    return JSONResponse(status_code=200, content=payload)
