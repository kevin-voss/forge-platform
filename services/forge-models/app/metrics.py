"""Prometheus usage metrics and in-memory aggregate snapshots."""

from __future__ import annotations

import logging
import math
import threading
import time
from collections import defaultdict, deque
from dataclasses import dataclass, field
from typing import Any, Callable

from prometheus_client import (
    CONTENT_TYPE_LATEST,
    CollectorRegistry,
    Counter,
    Gauge,
    Histogram,
    generate_latest,
)
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request
from starlette.responses import Response
from starlette.types import ASGIApp

logger = logging.getLogger("forge-models")

# Keep enough samples for a stable p95 without unbounded growth.
_LATENCY_SAMPLE_LIMIT = 512

_CAPABILITY_PATH_SUFFIX = {
    "embed": "embed",
    "generate": "generate",
    "classify": "classify",
    "summarize": "summarize",
}


@dataclass
class _ModelAgg:
    requests: int = 0
    tokens: int = 0
    errors: int = 0
    latencies_ms: deque[float] = field(default_factory=lambda: deque(maxlen=_LATENCY_SAMPLE_LIMIT))


class UsageMetrics:
    """In-process counters/histograms labeled by model + capability."""

    def __init__(
        self,
        *,
        enabled: bool = True,
        registry: CollectorRegistry | None = None,
    ) -> None:
        self.enabled = enabled
        self.registry = registry or CollectorRegistry()
        self._lock = threading.Lock()
        self._by_model: dict[str, _ModelAgg] = defaultdict(_ModelAgg)
        self._by_model_capability: dict[tuple[str, str], _ModelAgg] = defaultdict(_ModelAgg)

        # Capability-prefixed counters match 14.03 / manual verification names.
        label = ["model"]
        self._requests = {
            "embed": Counter(
                "models_embed_requests_total",
                "Total embed inference requests",
                label,
                registry=self.registry,
            ),
            "generate": Counter(
                "models_generate_requests_total",
                "Total generate inference requests",
                label,
                registry=self.registry,
            ),
            "classify": Counter(
                "models_classify_requests_total",
                "Total classify inference requests",
                label,
                registry=self.registry,
            ),
            "summarize": Counter(
                "models_summarize_requests_total",
                "Total summarize inference requests",
                label,
                registry=self.registry,
            ),
        }
        self._tokens = Counter(
            "models_tokens_total",
            "Approximate tokens processed by model inference",
            ["model", "capability"],
            registry=self.registry,
        )
        self._errors = Counter(
            "models_errors_total",
            "Inference errors by model and capability",
            ["model", "capability"],
            registry=self.registry,
        )
        self._latency = Histogram(
            "models_latency_seconds",
            "Inference latency in seconds",
            ["model", "capability"],
            buckets=(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0),
            registry=self.registry,
        )
        self._stream_active = Gauge(
            "models_stream_active",
            "Active SSE generate streams",
            registry=self.registry,
        )
        self._jobs_active = Gauge(
            "models_jobs_active",
            "Active async inference jobs",
            registry=self.registry,
        )

    def record(
        self,
        *,
        model: str,
        capability: str,
        latency_seconds: float,
        tokens: int = 0,
        error: bool = False,
    ) -> None:
        """Best-effort metric recording; never raises to callers."""
        if not self.enabled:
            return
        try:
            model_id = model or "unknown"
            cap = capability or "unknown"
            latency = max(0.0, float(latency_seconds))
            token_count = max(0, int(tokens))

            counter = self._requests.get(cap)
            if counter is not None:
                counter.labels(model=model_id).inc()
            if token_count:
                self._tokens.labels(model=model_id, capability=cap).inc(token_count)
            if error:
                self._errors.labels(model=model_id, capability=cap).inc()
            self._latency.labels(model=model_id, capability=cap).observe(latency)

            latency_ms = latency * 1000.0
            with self._lock:
                agg = self._by_model[model_id]
                agg.requests += 1
                agg.tokens += token_count
                if error:
                    agg.errors += 1
                agg.latencies_ms.append(latency_ms)

                key_agg = self._by_model_capability[(model_id, cap)]
                key_agg.requests += 1
                key_agg.tokens += token_count
                if error:
                    key_agg.errors += 1
                key_agg.latencies_ms.append(latency_ms)
        except Exception:  # noqa: BLE001 — metrics must never break inference
            logger.exception("metrics record failed (ignored)")

    def bump_stream_active(self, delta: int) -> None:
        if not self.enabled:
            return
        try:
            if delta >= 0:
                self._stream_active.inc(delta)
            else:
                self._stream_active.dec(abs(delta))
        except Exception:  # noqa: BLE001
            logger.exception("metrics stream gauge failed (ignored)")

    def set_jobs_active(self, value: int) -> None:
        if not self.enabled:
            return
        try:
            self._jobs_active.set(max(0, value))
        except Exception:  # noqa: BLE001
            logger.exception("metrics jobs gauge failed (ignored)")

    def bump_jobs_active(self, delta: int) -> None:
        if not self.enabled:
            return
        try:
            if delta >= 0:
                self._jobs_active.inc(delta)
            else:
                self._jobs_active.dec(abs(delta))
        except Exception:  # noqa: BLE001
            logger.exception("metrics jobs gauge failed (ignored)")

    def snapshot(self) -> dict[str, Any]:
        """Aggregate counters for GET /v1/usage."""
        with self._lock:
            by_model: dict[str, dict[str, Any]] = {}
            for model_id, agg in sorted(self._by_model.items()):
                by_model[model_id] = {
                    "requests": agg.requests,
                    "tokens": agg.tokens,
                    "errors": agg.errors,
                    "p95_latency_ms": _percentile(list(agg.latencies_ms), 95),
                }
            return {"by_model": by_model}

    def export_prometheus(self) -> tuple[bytes, str]:
        """Return Prometheus exposition body and content-type."""
        if not self.enabled:
            return b"# metrics disabled\n", CONTENT_TYPE_LATEST
        return generate_latest(self.registry), CONTENT_TYPE_LATEST


def _percentile(samples: list[float], pct: float) -> float:
    if not samples:
        return 0.0
    ordered = sorted(samples)
    if len(ordered) == 1:
        return round(ordered[0], 3)
    rank = (pct / 100.0) * (len(ordered) - 1)
    low = math.floor(rank)
    high = math.ceil(rank)
    if low == high:
        return round(ordered[low], 3)
    weight = rank - low
    return round(ordered[low] * (1.0 - weight) + ordered[high] * weight, 3)


def parse_inference_route(path: str) -> tuple[str, str] | None:
    """Return (model_id, capability) for inference paths, else None."""
    parts = path.strip("/").split("/")
    # v1 / models / {model} / {capability}
    if len(parts) != 4 or parts[0] != "v1" or parts[1] != "models":
        return None
    model_id = parts[2]
    capability = parts[3]
    if capability not in _CAPABILITY_PATH_SUFFIX or not model_id:
        return None
    return model_id, capability


class MetricsMiddleware(BaseHTTPMiddleware):
    """Record sync inference metrics after the handler returns.

    Streaming generate (`?stream=true`) is recorded in the SSE generator instead,
    because middleware would only observe response start latency.
    """

    def __init__(self, app: ASGIApp, metrics: UsageMetrics) -> None:
        super().__init__(app)
        self._metrics = metrics

    async def dispatch(self, request: Request, call_next: Callable) -> Response:
        route = parse_inference_route(request.url.path)
        if route is None or not self._metrics.enabled:
            return await call_next(request)

        model_id, capability = route
        stream = request.query_params.get("stream", "").lower() in {"1", "true", "yes"}
        if capability == "generate" and stream:
            return await call_next(request)

        started = time.perf_counter()
        response = await call_next(request)
        try:
            latency = time.perf_counter() - started
            status = getattr(response, "status_code", 500)
            error = status >= 400
            tokens = int(getattr(request.state, "usage_tokens", 0) or 0)
            # Prefer handler-set capability when present (e.g. job-driven paths).
            capability = getattr(request.state, "usage_capability", None) or capability
            model_id = getattr(request.state, "usage_model", None) or model_id
            self._metrics.record(
                model=model_id,
                capability=capability,
                latency_seconds=latency,
                tokens=tokens,
                error=error,
            )
        except Exception:  # noqa: BLE001 — never break inference for metrics
            logger.exception("metrics middleware failed (ignored)")
        return response


def redact_for_log(value: Any, *, max_chars: int = 256) -> Any:
    """Truncate oversized strings/lists for structured logs (never log full prompts)."""
    if isinstance(value, str):
        if len(value) <= max_chars:
            return value
        return f"{value[:max_chars]}…<{len(value)} chars>"
    if isinstance(value, list):
        if len(value) <= 4 and all(isinstance(item, str) and len(item) <= max_chars for item in value):
            return value
        return f"<list len={len(value)}>"
    if isinstance(value, dict):
        return {key: redact_for_log(item, max_chars=max_chars) for key, item in value.items()}
    return value
