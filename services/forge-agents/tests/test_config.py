"""Unit tests for Settings / config parsing."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.config import Settings, clear_settings_cache, get_settings


def test_parses_valid_env(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_LOG_LEVEL", "info")
    clean_env.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    settings = get_settings()
    assert settings.port == 4301
    assert settings.forge_log_level == "info"
    assert settings.forge_models_url == "http://forge-models:4300"
    assert settings.forge_service_name == "forge-agents"
    assert settings.forge_agents_tools_mode == "fake"
    assert settings.forge_agents_db_path == "/data/agents/runs.db"
    assert settings.forge_agents_max_concurrent_runs == 4


def test_accepts_db_path_and_concurrency(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_AGENTS_DB_PATH", "/tmp/agents.db")
    clean_env.setenv("FORGE_AGENTS_MAX_CONCURRENT_RUNS", "8")
    settings = get_settings()
    assert settings.forge_agents_db_path == "/tmp/agents.db"
    assert settings.forge_agents_max_concurrent_runs == 8


def test_accepts_tools_mode_live(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_AGENTS_TOOLS_MODE", "LIVE")
    settings = get_settings()
    assert settings.forge_agents_tools_mode == "live"


def test_rejects_bad_tools_mode(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_AGENTS_TOOLS_MODE", "hybrid")
    with pytest.raises(ValidationError):
        Settings()  # type: ignore[call-arg]


def test_defaults_models_url(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    settings = get_settings()
    assert settings.forge_models_url == "http://forge-models:4300"


def test_accepts_https_models_url(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_MODELS_URL", "https://models.example.internal")
    settings = get_settings()
    assert settings.forge_models_url == "https://models.example.internal"


def test_rejects_invalid_models_url(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "8080")
    clean_env.setenv("FORGE_MODELS_URL", "not-a-url")
    with pytest.raises(ValidationError) as exc:
        Settings()  # type: ignore[call-arg]
    assert "forge_models_url" in str(exc.value).lower() or "FORGE_MODELS_URL" in str(exc.value)


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
    clean_env.setenv("PORT", "4301")
    a = get_settings()
    b = get_settings()
    assert a is b
    clear_settings_cache()
    c = get_settings()
    assert c is not a
