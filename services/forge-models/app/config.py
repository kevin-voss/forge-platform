"""Typed environment configuration for forge-models."""

from __future__ import annotations

from functools import lru_cache
from typing import Literal

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

LogLevel = Literal["debug", "info", "warn", "error"]
ModelsBackend = Literal["fake", "local"]


class Settings(BaseSettings):
    """Process settings loaded from the environment."""

    model_config = SettingsConfigDict(
        env_file=None,
        case_sensitive=False,
        extra="ignore",
    )

    port: int = Field(..., alias="PORT", ge=1, le=65535)
    forge_log_level: LogLevel = Field(default="info", alias="FORGE_LOG_LEVEL")
    forge_models_backend: ModelsBackend = Field(default="fake", alias="FORGE_MODELS_BACKEND")
    forge_service_name: str = Field(default="forge-models", alias="FORGE_SERVICE_NAME")
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

    @field_validator("forge_models_backend", mode="before")
    @classmethod
    def normalize_backend(cls, value: object) -> object:
        if isinstance(value, str):
            return value.strip().lower()
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
