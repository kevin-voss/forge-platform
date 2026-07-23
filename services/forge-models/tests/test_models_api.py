"""Integration tests for GET /v1/models registry routes."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

from app.config import clear_settings_cache
from app.main import create_app
from app.registry import RegistryLoadError


@pytest.fixture
def models_client(env_valid: None, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
    config = tmp_path / "models.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "models": [
                    {
                        "id": "local-embed-small",
                        "backend": "local",
                        "capabilities": ["embed"],
                        "embedding_dim": 384,
                    },
                    {
                        "id": "local-general",
                        "backend": "fake",
                        "capabilities": ["generate", "classify", "summarize"],
                    },
                ]
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("FORGE_MODELS_CONFIG", str(config))
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as client:
        yield client
    clear_settings_cache()


def test_list_models_includes_embedding_dim(models_client: TestClient) -> None:
    resp = models_client.get("/v1/models")
    assert resp.status_code == 200
    body = resp.json()
    assert "models" in body
    by_id = {m["id"]: m for m in body["models"]}
    assert set(by_id) == {"local-embed-small", "local-general"}
    embed = by_id["local-embed-small"]
    assert embed["capabilities"] == ["embed"]
    assert embed["backend"] == "local"
    assert embed["embedding_dim"] == 384
    assert embed["status"] == "ok"
    general = by_id["local-general"]
    assert set(general["capabilities"]) == {"generate", "classify", "summarize"}
    assert general["embedding_dim"] is None


def test_get_model_ok(models_client: TestClient) -> None:
    resp = models_client.get("/v1/models/local-embed-small")
    assert resp.status_code == 200
    body = resp.json()
    assert body["id"] == "local-embed-small"
    assert body["embedding_dim"] == 384


def test_get_unknown_model_404(models_client: TestClient) -> None:
    resp = models_client.get("/v1/models/unknown")
    assert resp.status_code == 404
    body = resp.json()
    assert body["code"] == "model_not_found"
    assert "unknown" in body["error"]


def test_model_health_ok(models_client: TestClient) -> None:
    resp = models_client.get("/v1/models/local-general/health")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_model_health_unknown_404(models_client: TestClient) -> None:
    resp = models_client.get("/v1/models/nope/health")
    assert resp.status_code == 404
    assert resp.json()["code"] == "model_not_found"


def test_malformed_config_fails_create_app(
    clean_env: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    bad = tmp_path / "bad.yaml"
    bad.write_text("models: []\n", encoding="utf-8")
    clean_env.setenv("PORT", "4300")
    clean_env.setenv("FORGE_MODELS_BACKEND", "fake")
    clean_env.setenv("FORGE_MODELS_CONFIG", str(bad))
    with pytest.raises(RegistryLoadError, match="empty"):
        create_app()


def test_default_client_lists_packaged_models(client: TestClient) -> None:
    resp = client.get("/v1/models")
    assert resp.status_code == 200
    ids = {m["id"] for m in resp.json()["models"]}
    assert "local-embed-small" in ids
