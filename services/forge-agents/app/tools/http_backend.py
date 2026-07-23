"""Shared httpx helper for live platform tool backends."""

from __future__ import annotations

import logging
import time
from typing import Any

import httpx

from app.tools.errors import (
    ERROR_BACKEND_UNAVAILABLE,
    ERROR_TOOL_TIMEOUT,
    ToolError,
)
from app.tools.metrics import ToolMetrics, default_tool_metrics

logger = logging.getLogger("forge-agents")


class HttpBackend:
    """Thin async HTTP client with timeout + error normalization."""

    def __init__(
        self,
        base_url: str,
        *,
        timeout_seconds: float = 15.0,
        client: httpx.AsyncClient | None = None,
        metrics: ToolMetrics | None = None,
        service: str = "backend",
    ) -> None:
        self._base = base_url.rstrip("/")
        self._timeout = timeout_seconds
        self._owns_client = client is None
        self._client = client or httpx.AsyncClient(
            base_url=self._base,
            timeout=timeout_seconds,
        )
        self._metrics = metrics or default_tool_metrics
        self._service = service

    async def aclose(self) -> None:
        if self._owns_client:
            await self._client.aclose()

    async def request(
        self,
        method: str,
        path: str,
        *,
        tool: str,
        params: dict[str, Any] | None = None,
        json_body: dict[str, Any] | None = None,
        content: bytes | None = None,
        headers: dict[str, str] | None = None,
        expect_json: bool = True,
    ) -> httpx.Response:
        url = path if path.startswith("/") else f"/{path}"
        started = time.perf_counter()
        outcome = "ok"
        try:
            resp = await self._client.request(
                method,
                url,
                params=params,
                json=json_body,
                content=content,
                headers=headers,
            )
        except httpx.TimeoutException as exc:
            outcome = ERROR_TOOL_TIMEOUT
            raise ToolError(
                ERROR_TOOL_TIMEOUT,
                f"{tool} timed out calling {self._service}",
            ) from exc
        except httpx.HTTPError as exc:
            outcome = ERROR_BACKEND_UNAVAILABLE
            raise ToolError(
                ERROR_BACKEND_UNAVAILABLE,
                f"{tool} backend unavailable ({self._service}): {exc}",
            ) from exc
        finally:
            latency = time.perf_counter() - started
            self._metrics.record_backend_latency(tool, latency)
            logger.info(
                "tool backend call",
                extra={
                    "tool": tool,
                    "target_service": self._service,
                    "latency_seconds": round(latency, 4),
                    "outcome": outcome,
                    "method": method,
                    "path": url,
                },
            )

        if resp.status_code in {502, 503, 504}:
            raise ToolError(
                ERROR_BACKEND_UNAVAILABLE,
                f"{tool} backend unavailable ({self._service}): HTTP {resp.status_code}",
            )

        if expect_json and resp.status_code < 400:
            # Soft-check: callers may still read .text for binary.
            pass
        return resp
