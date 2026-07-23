"""Integration tests for generate / classify / summarize endpoints."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

from app.config import clear_settings_cache
from app.main import create_app


@pytest.fixture
def gen_client(env_valid: None, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
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
    monkeypatch.setenv("FORGE_MODELS_GEN_MAX_TOKENS", "64")
    monkeypatch.setenv("FORGE_MODELS_GEN_DEFAULT_TEMP", "0")
    monkeypatch.setenv("FORGE_MODELS_CLASSIFY_MAX_LABELS", "4")
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as client:
        yield client
    clear_settings_cache()


def test_generate_happy_path_shape(gen_client: TestClient) -> None:
    resp = gen_client.post(
        "/v1/models/local-general/generate",
        json={"prompt": "summarize: forge platform", "max_tokens": 32, "temperature": 0},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert isinstance(body["text"], str) and body["text"]
    assert body["finish_reason"] in {"stop", "length"}
    usage = body["usage"]
    assert set(usage) == {"prompt_tokens", "completion_tokens", "total_tokens"}
    assert usage["total_tokens"] == usage["prompt_tokens"] + usage["completion_tokens"]


def test_generate_temperature_zero_deterministic(gen_client: TestClient) -> None:
    payload = {"prompt": "hello forge", "max_tokens": 16, "temperature": 0}
    a = gen_client.post("/v1/models/local-general/generate", json=payload).json()
    b = gen_client.post("/v1/models/local-general/generate", json=payload).json()
    assert a == b


def test_classify_happy_path_sorted(gen_client: TestClient) -> None:
    resp = gen_client.post(
        "/v1/models/local-general/classify",
        json={
            "input": "database connection refused",
            "labels": ["network", "auth", "disk"],
        },
    )
    assert resp.status_code == 200
    labels = resp.json()["labels"]
    assert len(labels) == 3
    assert all("label" in item and "score" in item for item in labels)
    scores = [item["score"] for item in labels]
    assert scores == sorted(scores, reverse=True)


def test_classify_identical_label_scores_highest(gen_client: TestClient) -> None:
    resp = gen_client.post(
        "/v1/models/local-general/classify",
        json={"input": "auth", "labels": ["network", "auth", "disk"]},
    )
    assert resp.status_code == 200
    labels = resp.json()["labels"]
    assert labels[0]["label"] == "auth"
    assert labels[0]["score"] == 1.0


def test_summarize_happy_path_shorter(gen_client: TestClient) -> None:
    long_input = " ".join(f"incident-{i}" for i in range(40))
    resp = gen_client.post(
        "/v1/models/local-general/summarize",
        json={"input": long_input, "temperature": 0},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert isinstance(body["summary"], str) and body["summary"]
    assert len(body["summary"].split()) < len(long_input.split())
    assert set(body["usage"]) == {"prompt_tokens", "completion_tokens", "total_tokens"}


def test_capability_mismatch_embed_only_422(gen_client: TestClient) -> None:
    for path, payload in (
        ("/generate", {"prompt": "x"}),
        ("/classify", {"input": "x", "labels": ["a"]}),
        ("/summarize", {"input": "x"}),
    ):
        resp = gen_client.post(f"/v1/models/local-embed-small{path}", json=payload)
        assert resp.status_code == 422, path
        assert resp.json()["code"] == "capability_unsupported"


def test_unknown_model_404(gen_client: TestClient) -> None:
    resp = gen_client.post(
        "/v1/models/nope/generate",
        json={"prompt": "x", "temperature": 0},
    )
    assert resp.status_code == 404
    assert resp.json()["code"] == "model_not_found"


def test_max_tokens_over_cap_422(gen_client: TestClient) -> None:
    resp = gen_client.post(
        "/v1/models/local-general/generate",
        json={"prompt": "x", "max_tokens": 65, "temperature": 0},
    )
    assert resp.status_code == 422
    assert resp.json()["code"] == "invalid_params"
    assert "exceeds cap" in resp.json()["error"]


def test_empty_labels_422(gen_client: TestClient) -> None:
    resp = gen_client.post(
        "/v1/models/local-general/classify",
        json={"input": "x", "labels": []},
    )
    assert resp.status_code == 422
    assert resp.json()["code"] == "invalid_params"


def test_labels_too_many_422(gen_client: TestClient) -> None:
    resp = gen_client.post(
        "/v1/models/local-general/classify",
        json={"input": "x", "labels": ["a", "b", "c", "d", "e"]},
    )
    assert resp.status_code == 422
    assert resp.json()["code"] == "invalid_params"


def test_default_client_packaged_generate(client: TestClient) -> None:
    resp = client.post(
        "/v1/models/local-general/generate",
        json={"prompt": "packaged", "max_tokens": 16, "temperature": 0},
    )
    assert resp.status_code == 200
    assert "text" in resp.json()
