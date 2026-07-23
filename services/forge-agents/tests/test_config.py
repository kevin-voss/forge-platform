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
    assert settings.forge_control_url == "http://forge-control:4001"
    assert settings.forge_runtime_url == "http://forge-runtime:4102"
    assert settings.forge_observe_url == "http://forge-observe:4106"
    assert settings.forge_storage_url == "http://forge-storage:4107"
    assert settings.forge_events_url == "http://forge-events:4105"
    assert settings.forge_memory_url == "http://forge-memory:4303"
    assert settings.forge_agents_tool_timeout_seconds == 15.0
    assert settings.forge_service_name == "forge-agents"
    assert settings.forge_agents_tools_mode == "fake"
    assert settings.forge_agents_db_path == "/data/agents/runs.db"
    assert settings.forge_agents_max_concurrent_runs == 4
    assert settings.forge_agents_approval_ttl_seconds == 3600


def test_accepts_db_path_and_concurrency(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_AGENTS_DB_PATH", "/tmp/agents.db")
    clean_env.setenv("FORGE_AGENTS_MAX_CONCURRENT_RUNS", "8")
    clean_env.setenv("FORGE_AGENTS_APPROVAL_TTL_SECONDS", "120")
    settings = get_settings()
    assert settings.forge_agents_db_path == "/tmp/agents.db"
    assert settings.forge_agents_max_concurrent_runs == 8
    assert settings.forge_agents_approval_ttl_seconds == 120


def test_accepts_tools_mode_live(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_AGENTS_TOOLS_MODE", "LIVE")
    settings = get_settings()
    assert settings.forge_agents_tools_mode == "live"


def test_accepts_tool_backend_urls_and_timeout(clean_env: pytest.MonkeyPatch) -> None:
    clean_env.setenv("PORT", "4301")
    clean_env.setenv("FORGE_CONTROL_URL", "http://127.0.0.1:4001")
    clean_env.setenv("FORGE_RUNTIME_URL", "http://127.0.0.1:4102")
    clean_env.setenv("FORGE_OBSERVE_URL", "http://127.0.0.1:4106")
    clean_env.setenv("FORGE_STORAGE_URL", "http://127.0.0.1:4107")
    clean_env.setenv("FORGE_EVENTS_URL", "http://127.0.0.1:4105")
    clean_env.setenv("FORGE_MEMORY_URL", "http://127.0.0.1:4303")
    clean_env.setenv("FORGE_AGENTS_TOOL_TIMEOUT_SECONDS", "7.5")
    settings = get_settings()
    assert settings.forge_control_url == "http://127.0.0.1:4001"
    assert settings.forge_runtime_url == "http://127.0.0.1:4102"
    assert settings.forge_observe_url == "http://127.0.0.1:4106"
    assert settings.forge_storage_url == "http://127.0.0.1:4107"
    assert settings.forge_events_url == "http://127.0.0.1:4105"
    assert settings.forge_memory_url == "http://127.0.0.1:4303"
    assert settings.forge_agents_tool_timeout_seconds == 7.5


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
