"""Structured JSON logging and request-id middleware."""

from __future__ import annotations

import json
import logging
import sys
import uuid
from contextvars import ContextVar
from datetime import datetime, timezone
from typing import Any, Callable

from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request
from starlette.responses import Response
from starlette.types import ASGIApp

request_id_var: ContextVar[str | None] = ContextVar("request_id", default=None)

_LEVEL_RANK = {"debug": 10, "info": 20, "warn": 30, "error": 40}
_STD_TO_FORGE = {
    logging.DEBUG: "debug",
    logging.INFO: "info",
    logging.WARNING: "warn",
    logging.ERROR: "error",
    logging.CRITICAL: "error",
}


class JsonLogFormatter(logging.Formatter):
    """Emit one JSON object per line with Forge runtime-contract fields."""

    def __init__(self, service: str) -> None:
        super().__init__()
        self._service = service

    def format(self, record: logging.LogRecord) -> str:
        level = _STD_TO_FORGE.get(record.levelno, "info")
        payload: dict[str, Any] = {
            "timestamp": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "level": level,
            "service": self._service,
            "message": record.getMessage(),
        }
        rid = request_id_var.get()
        if rid:
            payload["request_id"] = rid
        for key in (
            "request_id",
            "path",
            "method",
            "status_code",
            "models_url",
            "signal",
            "agents_registry_size",
            "agent_names",
            "agents_defs_dir",
        ):
            if hasattr(record, key):
                value = getattr(record, key)
                if value is not None:
                    payload[key] = value
        return json.dumps(payload, separators=(",", ":"))


def configure_logging(service: str, level: str) -> logging.Logger:
    """Configure root + service logger for JSON stdout."""
    forge_level = level.lower()
    min_rank = _LEVEL_RANK.get(forge_level, 20)
    std_level = logging.DEBUG
    for name, rank in _LEVEL_RANK.items():
        if rank == min_rank:
            std_level = {
                "debug": logging.DEBUG,
                "info": logging.INFO,
                "warn": logging.WARNING,
                "error": logging.ERROR,
            }[name]
            break

    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(JsonLogFormatter(service))

    root = logging.getLogger()
    root.handlers.clear()
    root.addHandler(handler)
    root.setLevel(std_level)

    # Quiet noisy libraries; keep our app logs.
    logging.getLogger("uvicorn.access").handlers.clear()
    logging.getLogger("uvicorn.access").propagate = False

    logger = logging.getLogger("forge-agents")
    logger.setLevel(std_level)
    return logger


class RequestIdMiddleware(BaseHTTPMiddleware):
    """Propagate or mint X-Request-ID and attach it to structured logs."""

    def __init__(self, app: ASGIApp, logger: logging.Logger) -> None:
        super().__init__(app)
        self._logger = logger

    async def dispatch(self, request: Request, call_next: Callable) -> Response:
        incoming = request.headers.get("x-request-id", "").strip()
        request_id = incoming or str(uuid.uuid4())
        token = request_id_var.set(request_id)
        request.state.request_id = request_id
        try:
            response = await call_next(request)
        finally:
            request_id_var.reset(token)
        response.headers["X-Request-ID"] = request_id
        self._logger.info(
            "request completed",
            extra={
                "request_id": request_id,
                "method": request.method,
                "path": request.url.path,
                "status_code": response.status_code,
            },
        )
        return response
