"""Typed environment configuration for forge-agents."""

from __future__ import annotations

from functools import lru_cache
from typing import Literal
from urllib.parse import urlparse

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

LogLevel = Literal["debug", "info", "warn", "error"]


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
    forge_agents_defs_dir: str = Field(
        default="",
        alias="FORGE_AGENTS_DEFS_DIR",
        description="Directory of agent YAML definitions; empty uses packaged agents/",
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

    @field_validator("forge_models_url", mode="before")
    @classmethod
    def normalize_models_url(cls, value: object) -> object:
        if isinstance(value, str):
            return value.strip()
        return value

    @field_validator("forge_models_url")
    @classmethod
    def validate_models_url(cls, value: str) -> str:
        parsed = urlparse(value)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ValueError("FORGE_MODELS_URL must be an absolute http(s) URL")
        return value

    @field_validator("forge_agents_defs_dir", mode="before")
    @classmethod
    def normalize_defs_dir(cls, value: object) -> object:
        if value is None:
            return ""
        if isinstance(value, str):
            return value.strip()
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
