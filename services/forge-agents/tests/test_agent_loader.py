"""Unit tests for AgentLoader / load_registry."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from app.agents.loader import AgentLoadError, DEFAULT_AGENTS_DIR, load_registry


def _write_agent(directory: Path, filename: str, payload: object) -> Path:
    path = directory / filename
    path.write_text(yaml.safe_dump(payload), encoding="utf-8")
    return path


def test_loads_valid_directory(tmp_path: Path) -> None:
    _write_agent(
        tmp_path,
        "alpha.yaml",
        {
            "name": "alpha",
            "model": "local-general",
            "tools": ["echo.ping"],
            "permissions": ["project:read"],
            "limits": {"max_steps": 2, "timeout_seconds": 10},
        },
    )
    registry = load_registry(str(tmp_path))
    assert registry.agents_registry_size == 1
    assert registry.get("alpha") is not None
    assert registry.get("alpha").model == "local-general"


def test_default_packaged_fixture_loads() -> None:
    assert DEFAULT_AGENTS_DIR.is_dir()
    registry = load_registry(None)
    names = {a.name for a in registry.list()}
    assert "fixture-echo" in names
    for seed in (
        "deployment-investigator",
        "log-summarizer",
        "docs-assistant",
        "release-reviewer",
        "infra-health",
    ):
        assert seed in names
    echo = registry.get("fixture-echo")
    assert echo is not None
    assert echo.model == "local-general"


def test_malformed_yaml_raises_with_path(tmp_path: Path) -> None:
    bad = tmp_path / "bad.yaml"
    bad.write_text("name: [\n", encoding="utf-8")
    with pytest.raises(AgentLoadError) as exc:
        load_registry(str(tmp_path))
    assert "bad.yaml" in str(exc.value)
    assert "malformed" in str(exc.value).lower()


def test_duplicate_name_raises(tmp_path: Path) -> None:
    payload = {
        "name": "dup",
        "model": "local-general",
        "tools": ["echo.ping"],
        "permissions": ["project:read"],
        "limits": {"max_steps": 2, "timeout_seconds": 10},
    }
    _write_agent(tmp_path, "one.yaml", payload)
    _write_agent(tmp_path, "two.yaml", payload)
    with pytest.raises(AgentLoadError) as exc:
        load_registry(str(tmp_path))
    assert "duplicate" in str(exc.value).lower()
    assert "dup" in str(exc.value)


def test_limits_out_of_bounds_raises(tmp_path: Path) -> None:
    _write_agent(
        tmp_path,
        "huge.yaml",
        {
            "name": "huge",
            "model": "local-general",
            "tools": ["echo.ping"],
            "permissions": ["project:read"],
            "limits": {"max_steps": 9999, "timeout_seconds": 30},
        },
    )
    with pytest.raises(AgentLoadError) as exc:
        load_registry(str(tmp_path))
    assert "huge.yaml" in str(exc.value)


def test_missing_directory_raises(tmp_path: Path) -> None:
    missing = tmp_path / "nope"
    with pytest.raises(AgentLoadError) as exc:
        load_registry(str(missing))
    assert "not found" in str(exc.value).lower()
