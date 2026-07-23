"""Integration tests for run start/get/list/cancel APIs."""

from __future__ import annotations

import time
from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

from app.config import clear_settings_cache
from app.main import create_app

PROJECT = {"X-Forge-Project": "proj-a"}


def _write_agent(
    directory: Path,
    *,
    name: str = "fixture-echo",
    max_steps: int = 3,
    timeout_seconds: int = 30,
) -> None:
    payload = {
        "name": name,
        "model": "local-general",
        "tools": ["echo.ping"],
        "permissions": ["project:read"],
        "limits": {"max_steps": max_steps, "timeout_seconds": timeout_seconds},
    }
    (directory / f"{name}.yaml").write_text(yaml.safe_dump(payload), encoding="utf-8")


@pytest.fixture
def runs_client(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
    _write_agent(tmp_path, max_steps=3, timeout_seconds=30)
    _write_agent(tmp_path, name="loop-agent", max_steps=2, timeout_seconds=30)
    _write_agent(tmp_path, name="slow-agent", max_steps=10, timeout_seconds=1)
    monkeypatch.setenv("PORT", "4301")
    monkeypatch.setenv("FORGE_LOG_LEVEL", "error")
    monkeypatch.setenv("FORGE_MODELS_URL", "http://forge-models:4300")
    monkeypatch.setenv("FORGE_AGENTS_DEFS_DIR", str(tmp_path))
    monkeypatch.setenv("FORGE_AGENTS_TOOLS_MODE", "fake")
    monkeypatch.setenv("FORGE_AGENTS_DB_PATH", str(tmp_path / "runs.db"))
    monkeypatch.setenv("FORGE_AGENTS_MAX_CONCURRENT_RUNS", "4")
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as test_client:
        yield test_client
    clear_settings_cache()


def _wait_run(client: TestClient, run_id: str, *, timeout: float = 5.0) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        resp = client.get(f"/v1/runs/{run_id}", headers=PROJECT)
        assert resp.status_code == 200
        body = resp.json()
        if body["status"] != "running":
            return body
        time.sleep(0.02)
    raise AssertionError(f"run {run_id} did not terminate")


def test_dry_run_succeeds_with_recorded_steps(runs_client: TestClient) -> None:
    started = runs_client.post(
        "/v1/agents/fixture-echo/runs",
        headers=PROJECT,
        json={"input": "hello", "context": {"dry_run": True}},
    )
    assert started.status_code == 202
    run_id = started.json()["run_id"]
    assert started.json()["status"] == "running"

    body = _wait_run(runs_client, run_id)
    assert body["status"] == "succeeded"
    assert body["result"] == "hello"
    assert "steps" in body
    types = [s["type"] for s in body["steps"]]
    assert types == ["model", "tool", "model", "final"]

    listed = runs_client.get("/v1/runs", headers=PROJECT)
    assert listed.status_code == 200
    ids = {r["run_id"] for r in listed.json()["runs"]}
    assert run_id in ids


def test_force_loop_max_steps_exceeded(runs_client: TestClient) -> None:
    started = runs_client.post(
        "/v1/agents/loop-agent/runs",
        headers=PROJECT,
        json={"input": "x", "context": {"dry_run": True, "force_loop": True}},
    )
    assert started.status_code == 202
    body = _wait_run(runs_client, started.json()["run_id"])
    assert body["status"] == "stopped"
    assert body["error"] == "max_steps_exceeded"


def test_slow_tool_decide_times_out(runs_client: TestClient) -> None:
    started = runs_client.post(
        "/v1/agents/slow-agent/runs",
        headers=PROJECT,
        json={
            "input": "slow",
            "context": {"dry_run": True, "decide_delay_seconds": 3},
        },
    )
    assert started.status_code == 202
    body = _wait_run(runs_client, started.json()["run_id"], timeout=5.0)
    assert body["status"] == "failed"
    assert body["error"] == "timeout"


def test_cross_project_run_read_404(runs_client: TestClient) -> None:
    started = runs_client.post(
        "/v1/agents/fixture-echo/runs",
        headers=PROJECT,
        json={"input": "scoped", "context": {"dry_run": True}},
    )
    assert started.status_code == 202
    run_id = started.json()["run_id"]
    _wait_run(runs_client, run_id)

    other = runs_client.get(
        f"/v1/runs/{run_id}",
        headers={"X-Forge-Project": "proj-b"},
    )
    assert other.status_code == 404
    assert other.json()["code"] == "run_not_found"


def test_cancel_running_and_reject_terminal(runs_client: TestClient) -> None:
    started = runs_client.post(
        "/v1/agents/fixture-echo/runs",
        headers=PROJECT,
        json={
            "input": "cancel",
            "context": {"dry_run": True, "decide_delay_seconds": 2},
        },
    )
    assert started.status_code == 202
    run_id = started.json()["run_id"]

    cancelled = runs_client.post(f"/v1/runs/{run_id}/cancel", headers=PROJECT)
    assert cancelled.status_code == 200
    assert cancelled.json()["status"] == "cancelled"

    body = _wait_run(runs_client, run_id)
    assert body["status"] == "cancelled"

    again = runs_client.post(f"/v1/runs/{run_id}/cancel", headers=PROJECT)
    assert again.status_code == 409
    assert again.json()["code"] == "run_terminal"


def test_missing_project_header_400(runs_client: TestClient) -> None:
    resp = runs_client.post(
        "/v1/agents/fixture-echo/runs",
        json={"input": "x", "context": {"dry_run": True}},
    )
    assert resp.status_code == 400
    assert resp.json()["code"] == "project_required"
