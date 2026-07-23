"""POST /v1/models/{model}/embed — local embeddings inference."""

from __future__ import annotations

import logging
import time
from typing import Any

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from app.adapters.base import Capability
from app.adapters.local_embed import LocalEmbeddingAdapter
from app.config import Settings
from app.metrics import redact_for_log
from app.registry import ModelRegistry

logger = logging.getLogger("forge-models")

router = APIRouter(tags=["embeddings"])


class EmbedRequest(BaseModel):
    """Embed request body: a single string or a batch of strings."""

    input: str | list[str] = Field(..., description="Text or batch of texts to embed")


class EmbedUsage(BaseModel):
    input_count: int


class EmbedResponse(BaseModel):
    model: str
    embeddings: list[list[float]]
    dim: int
    usage: EmbedUsage


def _registry(request: Request) -> ModelRegistry:
    return request.app.state.registry


def _settings(request: Request) -> Settings:
    return request.app.state.settings


def _error(status: int, *, code: str, error: str) -> JSONResponse:
    return JSONResponse(status_code=status, content={"error": error, "code": code})


def _normalize_inputs(
    raw: str | list[str], *, max_batch: int, max_chars: int
) -> tuple[list[str] | None, JSONResponse | None]:
    if isinstance(raw, str):
        texts = [raw]
    else:
        texts = list(raw)

    if len(texts) == 0:
        return None, _error(
            422, code="invalid_input", error="input must be a non-empty string or list"
        )

    if len(texts) > max_batch:
        return None, _error(
            422,
            code="batch_too_large",
            error=f"batch size {len(texts)} exceeds max {max_batch}",
        )

    for index, text in enumerate(texts):
        if not isinstance(text, str):
            return None, _error(
                422,
                code="invalid_input",
                error=f"input[{index}] must be a string",
            )
        if text == "":
            return None, _error(
                422,
                code="invalid_input",
                error=f"input[{index}] must be a non-empty string",
            )
        if len(text) > max_chars:
            return None, _error(
                422,
                code="invalid_input",
                error=f"input[{index}] length {len(text)} exceeds max {max_chars} characters",
            )

    return texts, None


@router.post("/v1/models/{model_id}/embed")
async def embed_model(model_id: str, body: EmbedRequest, request: Request) -> JSONResponse:
    registry = _registry(request)
    settings = _settings(request)
    adapter = registry.get(model_id)
    if adapter is None:
        return _error(404, code="model_not_found", error=f"model not found: {model_id}")

    if Capability.EMBED not in adapter.capabilities:
        return _error(
            422,
            code="capability_unsupported",
            error=f"model '{model_id}' does not support embed",
        )

    if not isinstance(adapter, LocalEmbeddingAdapter):
        return _error(
            422,
            code="capability_unsupported",
            error=f"model '{model_id}' has no local embeddings adapter",
        )

    texts, err = _normalize_inputs(
        body.input,
        max_batch=settings.forge_models_embed_max_batch,
        max_chars=settings.forge_models_embed_max_chars,
    )
    if err is not None:
        return err
    assert texts is not None

    started = time.perf_counter()
    embeddings = adapter.embed(texts)
    latency_seconds = time.perf_counter() - started

    dim = adapter.embedding_dim
    for index, vector in enumerate(embeddings):
        if len(vector) != dim:
            logger.error(
                "embed dimension mismatch",
                extra={
                    "model": model_id,
                    "input_count": len(texts),
                    "index": index,
                    "got_dim": len(vector),
                    "expected_dim": dim,
                },
            )
            return _error(
                500,
                code="embed_dim_mismatch",
                error=f"produced embedding length {len(vector)} != registry dim {dim}",
            )

    metrics = registry.metrics
    metrics.record_embed(latency_seconds)
    request.state.usage_tokens = len(texts)
    request.state.usage_model = model_id
    request.state.usage_capability = "embed"

    logger.info(
        "embed completed",
        extra={
            "model": model_id,
            "input_count": len(texts),
            "latency_ms": round(latency_seconds * 1000.0, 3),
            "dim": dim,
            "embed_mode": adapter.embed_mode,
            "input_preview": redact_for_log(texts[0] if len(texts) == 1 else texts),
        },
    )

    payload: dict[str, Any] = {
        "model": model_id,
        "embeddings": embeddings,
        "dim": dim,
        "usage": {"input_count": len(texts)},
    }
    return JSONResponse(status_code=200, content=payload)
