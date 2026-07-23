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
    forge_models_config: str = Field(default="", alias="FORGE_MODELS_CONFIG")
    forge_models_embed_max_batch: int = Field(
        default=64,
        alias="FORGE_MODELS_EMBED_MAX_BATCH",
        ge=1,
        le=10_000,
    )
    forge_models_embed_max_chars: int = Field(
        default=8192,
        alias="FORGE_MODELS_EMBED_MAX_CHARS",
        ge=1,
        le=1_000_000,
    )
    forge_models_gen_max_tokens: int = Field(
        default=512,
        alias="FORGE_MODELS_GEN_MAX_TOKENS",
        ge=1,
        le=100_000,
    )
    forge_models_gen_default_temp: float = Field(
        default=0.0,
        alias="FORGE_MODELS_GEN_DEFAULT_TEMP",
        ge=0.0,
        le=2.0,
    )
    forge_models_classify_max_labels: int = Field(
        default=32,
        alias="FORGE_MODELS_CLASSIFY_MAX_LABELS",
        ge=1,
        le=1000,
    )
    forge_models_stream_timeout_seconds: int = Field(
        default=60,
        alias="FORGE_MODELS_STREAM_TIMEOUT_SECONDS",
        ge=1,
        le=3600,
    )
    forge_models_job_ttl_seconds: int = Field(
        default=3600,
        alias="FORGE_MODELS_JOB_TTL_SECONDS",
        ge=1,
        le=86400 * 7,
    )
    forge_models_max_concurrent_jobs: int = Field(
        default=4,
        alias="FORGE_MODELS_MAX_CONCURRENT_JOBS",
        ge=1,
        le=256,
    )
    forge_models_job_timeout_seconds: float = Field(
        default=300.0,
        alias="FORGE_MODELS_JOB_TIMEOUT_SECONDS",
        gt=0.0,
        le=86400.0,
    )
    forge_models_local_model_path: str = Field(default="", alias="FORGE_MODELS_LOCAL_MODEL_PATH")
    forge_models_metrics_enabled: bool = Field(
        default=True,
        alias="FORGE_MODELS_METRICS_ENABLED",
    )
    forge_otel_exporter_otlp_endpoint: str = Field(
        default="",
        alias="FORGE_OTEL_EXPORTER_OTLP_ENDPOINT",
    )
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

    @field_validator(
        "forge_models_config",
        "forge_models_local_model_path",
        "forge_otel_exporter_otlp_endpoint",
        mode="before",
    )
    @classmethod
    def strip_config_path(cls, value: object) -> object:
        if isinstance(value, str):
            return value.strip()
        return value

    @field_validator("forge_models_metrics_enabled", mode="before")
    @classmethod
    def normalize_bool(cls, value: object) -> object:
        if isinstance(value, str):
            lowered = value.strip().lower()
            if lowered in {"1", "true", "yes", "on"}:
                return True
            if lowered in {"0", "false", "no", "off"}:
                return False
        return value


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    """Load and cache settings. Raises ValidationError on invalid env."""
    return Settings()  # type: ignore[call-arg]


def clear_settings_cache() -> None:
    """Reset cached settings (tests)."""
    get_settings.cache_clear()
