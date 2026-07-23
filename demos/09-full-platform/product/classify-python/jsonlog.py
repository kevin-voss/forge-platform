"""Structured JSON logging to stdout (epic 01 required fields)."""

from __future__ import annotations

import json
import sys
from datetime import datetime, timezone
from typing import Any


_LEVEL_RANK = {"debug": 10, "info": 20, "warn": 30, "error": 40}


class Logger:
    def __init__(self, service: str, level: str) -> None:
        self._service = service
        self._min = _LEVEL_RANK.get(level.lower(), 20)

    def _emit(self, level: str, message: str, **fields: Any) -> None:
        if _LEVEL_RANK.get(level, 20) < self._min:
            return
        payload: dict[str, Any] = {
            "timestamp": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "level": level,
            "service": self._service,
            "message": message,
        }
        payload.update(fields)
        sys.stdout.write(json.dumps(payload, separators=(",", ":")) + "\n")
        sys.stdout.flush()

    def debug(self, message: str, **fields: Any) -> None:
        self._emit("debug", message, **fields)

    def info(self, message: str, **fields: Any) -> None:
        self._emit("info", message, **fields)

    def warn(self, message: str, **fields: Any) -> None:
        self._emit("warn", message, **fields)

    def error(self, message: str, **fields: Any) -> None:
        self._emit("error", message, **fields)
