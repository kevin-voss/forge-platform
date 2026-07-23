"""Unit tests for RegistryLoader and model serialization."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from app.adapters.base import Capability, HealthStatus
from app.adapters.fake import FakeAdapter
from app.registry import RegistryLoadError, load_registry, serialize_model


def _write_yaml(path: Path, payload: object) -> Path:
    path.write_text(yaml.safe_dump(payload), encoding="utf-8")
    return path


def test_load_valid_registry(tmp_path: Path) -> None:
    config = _write_yaml(
        tmp_path / "models.yaml",
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
        },
    )
    registry = load_registry(str(config))
    assert registry.metrics.models_registry_size == 2
    embed = registry.get("local-embed-small")
    assert embed is not None
    assert embed.backend == "local"
    assert embed.embedding_dim == 384
    assert Capability.EMBED in embed.capabilities
    assert embed.health() == HealthStatus.OK
    general = registry.get("local-general")
    assert general is not None
    assert general.embedding_dim is None
    assert Capability.GENERATE in general.capabilities


def test_default_packaged_models_yaml_loads() -> None:
    registry = load_registry(None)
    ids = {adapter.model_id for adapter in registry.list()}
    assert "local-embed-small" in ids
    assert "local-general" in ids


@pytest.mark.parametrize(
    "payload,fragment",
    [
        (None, "empty"),
        ([], "mapping"),
        ({}, "missing required key 'models'"),
        ({"models": "nope"}, "must be a list"),
        ({"models": []}, "empty"),
        ({"models": [{"backend": "fake", "capabilities": ["generate"]}]}, "id"),
        (
            {"models": [{"id": "x", "backend": "fake", "capabilities": ["teleport"]}]},
            "unknown value",
        ),
        (
            {
                "models": [
                    {"id": "x", "backend": "bogus", "capabilities": ["generate"]},
                ]
            },
            "backend",
        ),
        (
            {
                "models": [
                    {"id": "x", "backend": "local", "capabilities": ["embed"]},
                ]
            },
            "embedding_dim",
        ),
        (
            {
                "models": [
                    {
                        "id": "dup",
                        "backend": "fake",
                        "capabilities": ["generate"],
                    },
                    {
                        "id": "dup",
                        "backend": "fake",
                        "capabilities": ["classify"],
                    },
                ]
            },
            "duplicate",
        ),
    ],
)
def test_load_rejects_malformed(tmp_path: Path, payload: object, fragment: str) -> None:
    if payload is None:
        path = tmp_path / "empty.yaml"
        path.write_text("", encoding="utf-8")
    else:
        path = _write_yaml(tmp_path / "bad.yaml", payload)
    with pytest.raises(RegistryLoadError, match=fragment):
        load_registry(str(path))


def test_load_rejects_invalid_yaml_syntax(tmp_path: Path) -> None:
    path = tmp_path / "bad.yaml"
    path.write_text("models: [\n  - id: broken\n    backend: [", encoding="utf-8")
    with pytest.raises(RegistryLoadError, match="malformed"):
        load_registry(str(path))


def test_capability_serialization_sorted() -> None:
    adapter = FakeAdapter(
        model_id="local-general",
        backend="fake",
        capabilities=[Capability.SUMMARIZE, Capability.GENERATE, Capability.CLASSIFY],
    )
    body = serialize_model(adapter)
    assert body["capabilities"] == ["classify", "generate", "summarize"]
    assert body["embedding_dim"] is None
    assert body["status"] == "ok"


def test_adapter_health_surfaced_in_serialize() -> None:
    adapter = FakeAdapter(
        model_id="down-model",
        backend="fake",
        capabilities=[Capability.GENERATE],
        health_status=HealthStatus.DOWN,
    )
    assert serialize_model(adapter)["status"] == "down"
