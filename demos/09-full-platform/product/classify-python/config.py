"""Environment configuration for incident-classify."""

from __future__ import annotations

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Config:
    port: int
    service_name: str
    service_version: str
    log_level: str
    env: str
    capstone_break: bool = False


def _truthy(raw: str | None) -> bool:
    return (raw or "").strip().lower() in {"1", "true", "yes", "on"}


def load_config() -> Config:
    port_raw = os.environ.get("PORT", "").strip()
    if not port_raw:
        raise ValueError("PORT is required")
    try:
        port = int(port_raw)
    except ValueError as exc:
        raise ValueError(f"PORT must be an integer 1–65535, got {port_raw!r}") from exc
    if port < 1 or port > 65535:
        raise ValueError(f"PORT must be an integer 1–65535, got {port_raw!r}")

    level = os.environ.get("FORGE_LOG_LEVEL", "").strip().lower() or "info"
    if level not in {"debug", "info", "warn", "error"}:
        raise ValueError(f"FORGE_LOG_LEVEL must be debug|info|warn|error, got {level!r}")

    name = os.environ.get("FORGE_SERVICE_NAME", "").strip() or "incident-classify"
    version = os.environ.get("FORGE_SERVICE_VERSION", "").strip() or "0.1.0"
    env = os.environ.get("FORGE_ENV", "").strip() or "development"

    return Config(
        port=port,
        service_name=name,
        service_version=version,
        log_level=level,
        env=env,
        capstone_break=_truthy(os.environ.get("CAPSTONE_BREAK")),
    )
