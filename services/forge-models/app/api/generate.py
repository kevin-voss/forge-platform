"""POST /v1/models/{model}/generate — local text generation (sync or SSE stream)."""

from __future__ import annotations

import logging
import time
from typing import Any

from fastapi import APIRouter, Query, Request
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, Field

from app.adapters.base import Capability
from app.adapters.local_gen import LocalGenerationAdapter
from app.config import Settings
from app.jobs.store import JobStore
from app.registry import ModelRegistry
from app.streaming import sse_generate_events

logger = logging.getLogger("forge-models")

router = APIRouter(tags=["generation"])

# Default when request omits max_tokens (still subject to FORGE_MODELS_GEN_MAX_TOKENS).
_DEFAULT_MAX_TOKENS = 128


class GenerateRequest(BaseModel):
    prompt: str = Field(..., description="Prompt text to generate from")
    max_tokens: int | None = Field(
        default=None,
        description="Max completion tokens (capped by FORGE_MODELS_GEN_MAX_TOKENS)",
    )
    temperature: float | None = Field(
        default=None,
        description="Sampling temperature; 0 is deterministic",
    )


def _registry(request: Request) -> ModelRegistry:
    return request.app.state.registry


def _settings(request: Request) -> Settings:
    return request.app.state.settings


def _job_store(request: Request) -> JobStore | None:
    return getattr(request.app.state, "job_store", None)


def _error(status: int, *, code: str, error: str) -> JSONResponse:
    return JSONResponse(status_code=status, content={"error": error, "code": code})


def resolve_gen_adapter(
    request: Request, model_id: str, *, capability: Capability
) -> tuple[LocalGenerationAdapter | None, JSONResponse | None]:
    adapter = _registry(request).get(model_id)
    if adapter is None:
        return None, _error(404, code="model_not_found", error=f"model not found: {model_id}")
    if capability not in adapter.capabilities:
        return None, _error(
            422,
            code="capability_unsupported",
            error=f"model '{model_id}' does not support {capability.value}",
        )
    if not isinstance(adapter, LocalGenerationAdapter):
        return None, _error(
            422,
            code="capability_unsupported",
            error=f"model '{model_id}' has no local generation adapter",
        )
    return adapter, None


def validate_generate_params(
    *,
    max_tokens: int | None,
    temperature: float | None,
    settings: Settings,
) -> tuple[int, float] | JSONResponse:
    """Resolve and validate max_tokens / temperature against configured caps."""
    cap = settings.forge_models_gen_max_tokens
    # Default is 128, but never above the configured cap.
    resolved_max = min(_DEFAULT_MAX_TOKENS, cap) if max_tokens is None else max_tokens
    resolved_temp = settings.forge_models_gen_default_temp if temperature is None else temperature

    if not isinstance(resolved_max, int) or isinstance(resolved_max, bool) or resolved_max < 1:
        return _error(422, code="invalid_params", error="max_tokens must be a positive integer")
    if resolved_max > cap:
        return _error(
            422,
            code="invalid_params",
            error=f"max_tokens {resolved_max} exceeds cap {cap}",
        )
    if not isinstance(resolved_temp, (int, float)) or isinstance(resolved_temp, bool):
        return _error(422, code="invalid_params", error="temperature must be a number")
    if resolved_temp < 0.0 or resolved_temp > 2.0:
        return _error(
            422,
            code="invalid_params",
            error="temperature must be between 0 and 2 inclusive",
        )
    return resolved_max, float(resolved_temp)


@router.post("/v1/models/{model_id}/generate", response_model=None)
async def generate_model(
    model_id: str,
    body: GenerateRequest,
    request: Request,
    stream: bool = Query(default=False, description="When true, stream SSE token chunks"),
) -> JSONResponse | StreamingResponse:
    settings = _settings(request)
    adapter, err = resolve_gen_adapter(request, model_id, capability=Capability.GENERATE)
    if err is not None:
        return err
    assert adapter is not None

    if not isinstance(body.prompt, str) or body.prompt == "":
        return _error(422, code="invalid_params", error="prompt must be a non-empty string")

    params = validate_generate_params(
        max_tokens=body.max_tokens,
        temperature=body.temperature,
        settings=settings,
    )
    if isinstance(params, JSONResponse):
        return params
    max_tokens, temperature = params

    if stream:
        store = _job_store(request)

        def _on_active(delta: int) -> None:
            if store is not None:
                store.metrics.bump_stream_active(delta)

        logger.info("stream started", extra={"model": model_id})
        return StreamingResponse(
            sse_generate_events(
                adapter,
                body.prompt,
                max_tokens=max_tokens,
                temperature=temperature,
                model_id=model_id,
                timeout_seconds=float(settings.forge_models_stream_timeout_seconds),
                on_active=_on_active,
            ),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
                "X-Accel-Buffering": "no",
            },
        )

    started = time.perf_counter()
    result = adapter.generate(body.prompt, max_tokens=max_tokens, temperature=temperature)
    latency_seconds = time.perf_counter() - started

    metrics = _registry(request).metrics
    metrics.record_generate(latency_seconds)

    logger.info(
        "generate completed",
        extra={
            "model": model_id,
            "prompt_tokens": result.usage.prompt_tokens,
            "completion_tokens": result.usage.completion_tokens,
            "total_tokens": result.usage.total_tokens,
            "finish_reason": result.finish_reason,
            "latency_ms": round(latency_seconds * 1000.0, 3),
        },
    )

    payload: dict[str, Any] = {
        "text": result.text,
        "finish_reason": result.finish_reason,
        "usage": result.usage.as_dict(),
    }
    return JSONResponse(status_code=200, content=payload)
