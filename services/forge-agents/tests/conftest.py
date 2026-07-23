"""Shared pytest fixtures for forge-agents."""

from __future__ import annotations

import os

import pytest
from fastapi.testclient import TestClient

from app.config import clear_settings_cache
from app.main import create_app


@pytest.fixture
def env_valid(monkeypatch: pytest.MonkeyPatch, tmp_path) -> None:
    monkeypatch.setenv("PORT", "4301")
    monkeypatch.setenv("FORGE_LOG_LEVEL", "error")
    monkeypatch.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    monkeypatch.setenv("FORGE_SERVICE_NAME", "forge-agents")
    monkeypatch.setenv("FORGE_SERVICE_VERSION", "0.1.0")
    monkeypatch.setenv("FORGE_AGENTS_DB_PATH", str(tmp_path / "runs.db"))
    monkeypatch.setenv("FORGE_AGENTS_MAX_CONCURRENT_RUNS", "4")
    clear_settings_cache()


@pytest.fixture
def client(env_valid: None) -> TestClient:
    application = create_app()
    with TestClient(application) as test_client:
        yield test_client
    clear_settings_cache()


@pytest.fixture
def clean_env(monkeypatch: pytest.MonkeyPatch) -> pytest.MonkeyPatch:
    for key in list(os.environ):
        if key.startswith("FORGE_") or key == "PORT":
            monkeypatch.delenv(key, raising=False)
    clear_settings_cache()
    yield monkeypatch
    clear_settings_cache()
