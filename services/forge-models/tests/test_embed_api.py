"""Integration tests for POST /v1/models/{model}/embed."""

from __future__ import annotations

import math
from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

from app.config import clear_settings_cache
from app.main import create_app


@pytest.fixture
def embed_client(env_valid: None, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
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
    monkeypatch.setenv("FORGE_MODELS_EMBED_MAX_BATCH", "4")
    monkeypatch.setenv("FORGE_MODELS_EMBED_MAX_CHARS", "32")
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as client:
        yield client
    clear_settings_cache()


def test_embed_single_shape_and_dim(embed_client: TestClient) -> None:
    resp = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": "hello forge"},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["model"] == "local-embed-small"
    assert body["dim"] == 384
    assert body["usage"] == {"input_count": 1}
    assert len(body["embeddings"]) == 1
    vec = body["embeddings"][0]
    assert len(vec) == 384
    norm = math.sqrt(sum(v * v for v in vec))
    assert abs(norm - 1.0) < 1e-6


def test_embed_batch_shape(embed_client: TestClient) -> None:
    resp = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": ["a", "b", "c"]},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["usage"]["input_count"] == 3
    assert len(body["embeddings"]) == 3
    assert all(len(v) == body["dim"] for v in body["embeddings"])
    assert body["embeddings"][0] != body["embeddings"][1]


def test_embed_deterministic(embed_client: TestClient) -> None:
    a = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": "x"},
    ).json()
    b = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": "x"},
    ).json()
    assert a == b


def test_embed_unknown_model_404(embed_client: TestClient) -> None:
    resp = embed_client.post("/v1/models/nope/embed", json={"input": "x"})
    assert resp.status_code == 404
    assert resp.json()["code"] == "model_not_found"


def test_embed_non_embed_model_422(embed_client: TestClient) -> None:
    resp = embed_client.post(
        "/v1/models/local-general/embed",
        json={"input": "hello"},
    )
    assert resp.status_code == 422
    assert resp.json()["code"] == "capability_unsupported"


def test_embed_batch_too_large_422(embed_client: TestClient) -> None:
    resp = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": ["a", "b", "c", "d", "e"]},
    )
    assert resp.status_code == 422
    assert resp.json()["code"] == "batch_too_large"


def test_embed_oversized_input_422(embed_client: TestClient) -> None:
    resp = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": "x" * 33},
    )
    assert resp.status_code == 422
    assert resp.json()["code"] == "invalid_input"


def test_embed_empty_string_422(embed_client: TestClient) -> None:
    resp = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": ""},
    )
    assert resp.status_code == 422


def test_embed_empty_batch_422(embed_client: TestClient) -> None:
    resp = embed_client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": []},
    )
    assert resp.status_code == 422


def test_default_client_embed_packaged_model(client: TestClient) -> None:
    resp = client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": "packaged"},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["dim"] == 384
    assert len(body["embeddings"][0]) == 384
