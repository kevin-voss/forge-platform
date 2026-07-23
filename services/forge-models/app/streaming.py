"""SSE helpers for streamed generation responses."""

from __future__ import annotations

import asyncio
import json
import logging
import time
from collections.abc import AsyncIterator, Callable
from typing import Any

from app.adapters.local_gen import LocalGenerationAdapter
from app.metrics import UsageMetrics

logger = logging.getLogger("forge-models")

DONE_SENTINEL = "[DONE]"


def format_sse_data(payload: str | dict[str, Any]) -> str:
    """Format one SSE ``data:`` event line (with trailing blank line)."""
    if isinstance(payload, dict):
        body = json.dumps(payload, separators=(",", ":"))
    else:
        body = payload
    return f"data: {body}\n\n"


def chunk_text(text: str) -> list[str]:
    """Split generated text into incremental word-sized deltas."""
    if not text:
        return []
    words = text.split(" ")
    chunks: list[str] = []
    for index, word in enumerate(words):
        chunks.append(word if index == 0 else f" {word}")
    return chunks


async def iter_generate_deltas(
    adapter: LocalGenerationAdapter,
    prompt: str,
    *,
    max_tokens: int,
    temperature: float,
    cancelled: Callable[[], bool] | None = None,
) -> AsyncIterator[str]:
    """Yield text deltas from a local generate call (cooperative cancel)."""
    # Run sync generate off the event loop so cancel/disconnect can interleave.
    result = await asyncio.to_thread(
        adapter.generate,
        prompt,
        max_tokens=max_tokens,
        temperature=temperature,
    )
    for delta in chunk_text(result.text):
        if cancelled is not None and cancelled():
            return
        yield delta
        await asyncio.sleep(0)


async def sse_generate_events(
    adapter: LocalGenerationAdapter,
    prompt: str,
    *,
    max_tokens: int,
    temperature: float,
    model_id: str,
    timeout_seconds: float,
    on_active: Callable[[int], None] | None = None,
    usage_metrics: UsageMetrics | None = None,
) -> AsyncIterator[str]:
    """Async generator of SSE frames ending with ``data: [DONE]``."""
    started = time.perf_counter()
    chunks = 0
    cancelled = False
    timed_out = False

    if on_active is not None:
        on_active(1)

    try:
        async with asyncio.timeout(timeout_seconds):
            async for delta in iter_generate_deltas(
                adapter,
                prompt,
                max_tokens=max_tokens,
                temperature=temperature,
                cancelled=lambda: cancelled,
            ):
                chunks += 1
                yield format_sse_data({"delta": delta})
            yield format_sse_data(DONE_SENTINEL)
    except TimeoutError:
        timed_out = True
        logger.warning(
            "stream timed out",
            extra={"model": model_id, "chunks": chunks, "timeout_seconds": timeout_seconds},
        )
        yield format_sse_data({"error": "timeout", "code": "timeout"})
        yield format_sse_data(DONE_SENTINEL)
    except asyncio.CancelledError:
        cancelled = True
        logger.info(
            "stream cancelled (client disconnect)",
            extra={"model": model_id, "chunks": chunks},
        )
        raise
    finally:
        if on_active is not None:
            on_active(-1)
        duration = time.perf_counter() - started
        duration_ms = round(duration * 1000.0, 3)
        if usage_metrics is not None:
            usage_metrics.record(
                model=model_id,
                capability="generate",
                latency_seconds=duration,
                tokens=max(0, chunks),
                error=timed_out or cancelled,
            )
        logger.info(
            "stream ended",
            extra={"model": model_id, "chunks": chunks, "duration_ms": duration_ms},
        )
