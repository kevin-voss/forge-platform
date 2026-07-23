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
