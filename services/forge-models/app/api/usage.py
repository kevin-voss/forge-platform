"""GET /v1/usage — aggregate in-memory usage snapshot."""

from __future__ import annotations

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse

from app.metrics import UsageMetrics

router = APIRouter(tags=["usage"])


@router.get("/v1/usage")
async def get_usage(request: Request) -> JSONResponse:
    metrics: UsageMetrics | None = getattr(request.app.state, "usage_metrics", None)
    if metrics is None:
        return JSONResponse(status_code=200, content={"by_model": {}})
    return JSONResponse(status_code=200, content=metrics.snapshot())
