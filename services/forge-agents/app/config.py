"""Typed environment configuration for forge-agents."""

from __future__ import annotations

from functools import lru_cache
from typing import Literal
from urllib.parse import urlparse

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

LogLevel = Literal["debug", "info", "warn", "error"]


def _validate_http_url(value: str, *, name: str) -> str:
    parsed = urlparse(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError(f"{name} must be an absolute http(s) URL")
    return value


class Settings(BaseSettings):
    """Process settings loaded from the environment."""

    model_config = SettingsConfigDict(
        env_file=None,
        case_sensitive=False,
        extra="ignore",
    )

    port: int = Field(..., alias="PORT", ge=1, le=65535)
    forge_log_level: LogLevel = Field(default="info", alias="FORGE_LOG_LEVEL")
    forge_models_url: str = Field(
        default="http://forge-models:4300",
        alias="FORGE_MODELS_URL",
    )
    forge_control_url: str = Field(
        default="http://forge-control:4001",
        alias="FORGE_CONTROL_URL",
    )
    forge_runtime_url: str = Field(
        default="http://forge-runtime:4102",
        alias="FORGE_RUNTIME_URL",
    )
    forge_observe_url: str = Field(
        default="http://forge-observe:4106",
        alias="FORGE_OBSERVE_URL",
    )
    forge_storage_url: str = Field(
        default="http://forge-storage:4107",
        alias="FORGE_STORAGE_URL",
    )
    forge_events_url: str = Field(
        default="http://forge-events:4105",
        alias="FORGE_EVENTS_URL",
    )
    forge_agents_defs_dir: str = Field(
        default="",
        alias="FORGE_AGENTS_DEFS_DIR",
        description="Directory of agent YAML definitions; empty uses packaged agents/",
    )
    forge_agents_tools_mode: Literal["fake", "live"] = Field(
        default="fake",
        alias="FORGE_AGENTS_TOOLS_MODE",
        description="Tool backend mode: fake fixtures (CI default) or live HTTP adapters",
    )
    forge_agents_tool_timeout_seconds: float = Field(
        default=15.0,
        alias="FORGE_AGENTS_TOOL_TIMEOUT_SECONDS",
        ge=0.1,
        le=300.0,
        description="Per-tool HTTP timeout for live backends",
    )
    forge_agents_db_path: str = Field(
        default="/data/agents/runs.db",
        alias="FORGE_AGENTS_DB_PATH",
        description="SQLite path for run + step audit history",
    )
    forge_agents_max_concurrent_runs: int = Field(
        default=4,
        alias="FORGE_AGENTS_MAX_CONCURRENT_RUNS",
        ge=1,
        le=256,
        description="Hard cap on concurrent in-flight agent runs",
    )
    forge_service_name: str = Field(default="forge-agents", alias="FORGE_SERVICE_NAME")
    forge_service_version: str = Field(default="0.1.0", alias="FORGE_SERVICE_VERSION")
    forge_env: str = Field(default="development", alias="FORGE_ENV")
    forge_shutdown_grace_seconds: int = Field(
        default=10,
        alias="FORGE_SHUTDOWN_GRACE_SECONDS",
        ge=1,
        le=300,
    )

    @field_validator("forge_log_level", mode="before")
    @classmethod
    def normalize_log_level(cls, value: object) -> object:
        if isinstance(value, str):
            return value.strip().lower()
        return value

    @field_validator(
        "forge_models_url",
        "forge_control_url",
        "forge_runtime_url",
        "forge_observe_url",
        "forge_storage_url",
        "forge_events_url",
        mode="before",
    )
    @classmethod
    def normalize_service_urls(cls, value: object) -> object:
        if isinstance(value, str):
            return value.strip()
        return value

    @field_validator("forge_models_url")
    @classmethod
    def validate_models_url(cls, value: str) -> str:
        return _validate_http_url(value, name="FORGE_MODELS_URL")

    @field_validator("forge_control_url")
    @classmethod
    def validate_control_url(cls, value: str) -> str:
        return _validate_http_url(value, name="FORGE_CONTROL_URL")

    @field_validator("forge_runtime_url")
    @classmethod
    def validate_runtime_url(cls, value: str) -> str:
        return _validate_http_url(value, name="FORGE_RUNTIME_URL")

    @field_validator("forge_observe_url")
    @classmethod
    def validate_observe_url(cls, value: str) -> str:
        return _validate_http_url(value, name="FORGE_OBSERVE_URL")

    @field_validator("forge_storage_url")
    @classmethod
    def validate_storage_url(cls, value: str) -> str:
        return _validate_http_url(value, name="FORGE_STORAGE_URL")

    @field_validator("forge_events_url")
    @classmethod
    def validate_events_url(cls, value: str) -> str:
        return _validate_http_url(value, name="FORGE_EVENTS_URL")

    @field_validator("forge_agents_defs_dir", mode="before")
    @classmethod
    def normalize_defs_dir(cls, value: object) -> object:
        if value is None:
            return ""
        if isinstance(value, str):
            return value.strip()
        return value

    @field_validator("forge_agents_tools_mode", mode="before")
    @classmethod
    def normalize_tools_mode(cls, value: object) -> object:
        if isinstance(value, str):
            return value.strip().lower()
        return value

    @field_validator("forge_agents_db_path", mode="before")
    @classmethod
    def normalize_db_path(cls, value: object) -> object:
        if isinstance(value, str):
            stripped = value.strip()
            return stripped if stripped else "/data/agents/runs.db"
        return value

    @field_validator("forge_service_name", "forge_service_version", "forge_env", mode="before")
    @classmethod
    def strip_strings(cls, value: object) -> object:
        if isinstance(value, str):
            stripped = value.strip()
            return stripped if stripped else None
        return value


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    """Load and cache settings. Raises ValidationError on invalid env."""
    return Settings()  # type: ignore[call-arg]


def clear_settings_cache() -> None:
    """Reset cached settings (tests)."""
    get_settings.cache_clear()
