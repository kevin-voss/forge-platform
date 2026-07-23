"""Unit tests for Settings / config parsing."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.config import Settings, clear_settings_cache, get_settings


def test_parses_valid_env(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4300")
    clean_env.setenv("FORGE_LOG_LEVEL", "info")
    clean_env.setenv("FORGE_MODELS_BACKEND", "fake")
    settings = get_settings()
    assert settings.port == 4300
    assert settings.forge_log_level == "info"
    assert settings.forge_models_backend == "fake"
    assert settings.forge_service_name == "forge-models"


def test_defaults_backend_to_fake(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    settings = get_settings()
    assert settings.forge_models_backend == "fake"
    assert settings.forge_models_embed_max_batch == 64
    assert settings.forge_models_embed_max_chars == 8192
    assert settings.forge_models_gen_max_tokens == 512
    assert settings.forge_models_gen_default_temp == 0.0
    assert settings.forge_models_classify_max_labels == 32
    assert settings.forge_models_stream_timeout_seconds == 60
    assert settings.forge_models_job_ttl_seconds == 3600
    assert settings.forge_models_max_concurrent_jobs == 4
    assert settings.forge_models_job_timeout_seconds == 300.0
    assert settings.forge_models_local_model_path == ""


def test_embed_limits_from_env(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_MODELS_EMBED_MAX_BATCH", "8")
    clean_env.setenv("FORGE_MODELS_EMBED_MAX_CHARS", "128")
    clean_env.setenv("FORGE_MODELS_LOCAL_MODEL_PATH", " /tmp/model ")
    settings = get_settings()
    assert settings.forge_models_embed_max_batch == 8
    assert settings.forge_models_embed_max_chars == 128
    assert settings.forge_models_local_model_path == "/tmp/model"


def test_gen_limits_from_env(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_MODELS_GEN_MAX_TOKENS", "256")
    clean_env.setenv("FORGE_MODELS_GEN_DEFAULT_TEMP", "0.5")
    clean_env.setenv("FORGE_MODELS_CLASSIFY_MAX_LABELS", "8")
    settings = get_settings()
    assert settings.forge_models_gen_max_tokens == 256
    assert settings.forge_models_gen_default_temp == 0.5
    assert settings.forge_models_classify_max_labels == 8


def test_stream_and_job_limits_from_env(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_MODELS_STREAM_TIMEOUT_SECONDS", "30")
    clean_env.setenv("FORGE_MODELS_JOB_TTL_SECONDS", "120")
    clean_env.setenv("FORGE_MODELS_MAX_CONCURRENT_JOBS", "8")
    clean_env.setenv("FORGE_MODELS_JOB_TIMEOUT_SECONDS", "45.5")
    settings = get_settings()
    assert settings.forge_models_stream_timeout_seconds == 30
    assert settings.forge_models_job_ttl_seconds == 120
    assert settings.forge_models_max_concurrent_jobs == 8
    assert settings.forge_models_job_timeout_seconds == 45.5


def test_metrics_flags_from_env(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    settings = get_settings()
    assert settings.forge_models_metrics_enabled is True
    assert settings.forge_otel_exporter_otlp_endpoint == ""

    clear_settings_cache()
    clean_env.setenv("FORGE_MODELS_METRICS_ENABLED", "false")
    clean_env.setenv("FORGE_OTEL_EXPORTER_OTLP_ENDPOINT", " http://otel:4317 ")
    settings = get_settings()
    assert settings.forge_models_metrics_enabled is False
    assert settings.forge_otel_exporter_otlp_endpoint == "http://otel:4317"


def test_accepts_local_backend(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_MODELS_BACKEND", "local")
    settings = get_settings()
    assert settings.forge_models_backend == "local"


def test_rejects_unknown_backend(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_MODELS_BACKEND", "openai")
    with pytest.raises(ValidationError) as exc:
        Settings()  # type: ignore[call-arg]
    assert "forge_models_backend" in str(exc.value).lower() or "FORGE_MODELS_BACKEND" in str(
        exc.value
    )


def test_rejects_invalid_port(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "not-a-port")
    with pytest.raises(ValidationError):
        Settings()  # type: ignore[call-arg]


def test_rejects_missing_port(clean_env: pytest.MonkeyPatch) -> None:
    with pytest.raises(ValidationError):
        Settings()  # type: ignore[call-arg]


def test_rejects_bad_log_level(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_LOG_LEVEL", "verbose")
    with pytest.raises(ValidationError):
        Settings()  # type: ignore[call-arg]


def test_get_settings_cache(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4300")
    a = get_settings()
    b = get_settings()
    assert a is b
    clear_settings_cache()
    c = get_settings()
    assert c is not a
