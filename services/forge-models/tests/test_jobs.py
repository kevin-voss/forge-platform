"""Unit + integration tests for async job store, worker, and API."""

from __future__ import annotations

import time
from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

from app.config import clear_settings_cache
from app.jobs.store import InvalidTransitionError, JobStatus, JobStore
from app.main import create_app


def test_job_state_machine_valid_and_invalid() -> None:
    store = JobStore(ttl_seconds=60)
    job = store.create(
        project_id="proj-a",
        model="local-general",
        task="summarize",
        input_payload="hello",
    )
    assert job.status == JobStatus.QUEUED
    store.transition(job.id, JobStatus.RUNNING)
    store.transition(job.id, JobStatus.SUCCEEDED, result={"summary": "ok"})
    with pytest.raises(InvalidTransitionError):
        store.transition(job.id, JobStatus.CANCELLED)
    with pytest.raises(InvalidTransitionError):
        store.transition(job.id, JobStatus.RUNNING)


def test_cancel_queued_sets_cancelled() -> None:
    store = JobStore(ttl_seconds=60)
    job = store.create(
        project_id="proj-a",
        model="m",
        task="generate",
        input_payload="x",
    )
    outcome = store.request_cancel(job.id, project_id="proj-a")
    assert outcome is not None and outcome != "terminal"
    assert outcome.status == JobStatus.CANCELLED
    assert store.request_cancel(job.id, project_id="proj-a") == "terminal"


@pytest.fixture
def jobs_client(env_valid: None, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> TestClient:
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
    monkeypatch.setenv("FORGE_MODELS_JOB_TIMEOUT_SECONDS", "2")
    monkeypatch.setenv("FORGE_MODELS_MAX_CONCURRENT_JOBS", "2")
    monkeypatch.setenv("FORGE_MODELS_JOB_TTL_SECONDS", "3600")
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as client:
        yield client
    clear_settings_cache()


def _headers(project: str = "proj-a") -> dict[str, str]:
    return {"X-Forge-Project": project, "content-type": "application/json"}


def _poll_terminal(client: TestClient, job_id: str, *, project: str = "proj-a") -> dict:
    deadline = time.time() + 5.0
    while time.time() < deadline:
        resp = client.get(f"/v1/jobs/{job_id}", headers=_headers(project))
        assert resp.status_code == 200
        body = resp.json()
        if body["status"] in {"succeeded", "failed", "cancelled"}:
            return body
        time.sleep(0.05)
    raise AssertionError(f"job {job_id} did not reach terminal state")


def test_submit_poll_succeeded(jobs_client: TestClient) -> None:
    resp = jobs_client.post(
        "/v1/jobs",
        headers=_headers(),
        json={
            "model": "local-general",
            "task": "summarize",
            "input": "long incident text about database connection refused",
        },
    )
    assert resp.status_code == 202
    body = resp.json()
    assert body["status"] == "queued"
    assert "job_id" in body

    final = _poll_terminal(jobs_client, body["job_id"])
    assert final["status"] == "succeeded"
    assert "summary" in final["result"]
    assert "usage" in final["result"]


def test_cancel_mid_run(jobs_client: TestClient) -> None:
    resp = jobs_client.post(
        "/v1/jobs",
        headers=_headers(),
        json={
            "model": "local-general",
            "task": "summarize",
            "input": "slow job text",
            "delay_ms": 2000,
        },
    )
    assert resp.status_code == 202
    job_id = resp.json()["job_id"]

    # Wait until running (or still queued — cancel works either way).
    deadline = time.time() + 2.0
    while time.time() < deadline:
        status = jobs_client.get(f"/v1/jobs/{job_id}", headers=_headers()).json()["status"]
        if status == "running":
            break
        time.sleep(0.02)

    cancel = jobs_client.delete(f"/v1/jobs/{job_id}", headers=_headers())
    assert cancel.status_code == 200
    assert cancel.json()["status"] == "cancelled"

    final = _poll_terminal(jobs_client, job_id)
    assert final["status"] == "cancelled"

    again = jobs_client.delete(f"/v1/jobs/{job_id}", headers=_headers())
    assert again.status_code == 409
    assert again.json()["code"] == "job_terminal"


def test_timeout_job_failed(
    env_valid: None, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    config = tmp_path / "models.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "models": [
                    {
                        "id": "local-general",
                        "backend": "fake",
                        "capabilities": ["generate", "classify", "summarize"],
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("FORGE_MODELS_CONFIG", str(config))
    monkeypatch.setenv("FORGE_MODELS_JOB_TIMEOUT_SECONDS", "0.15")
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as client:
        resp = client.post(
            "/v1/jobs",
            headers=_headers(),
            json={
                "model": "local-general",
                "task": "summarize",
                "input": "timeout me",
                "delay_ms": 2000,
            },
        )
        assert resp.status_code == 202
        final = _poll_terminal(client, resp.json()["job_id"])
        assert final["status"] == "failed"
        assert final["error"]["code"] == "timeout"
    clear_settings_cache()


def test_cross_project_job_read_404(jobs_client: TestClient) -> None:
    resp = jobs_client.post(
        "/v1/jobs",
        headers=_headers("proj-a"),
        json={"model": "local-general", "task": "generate", "input": {"prompt": "x"}},
    )
    job_id = resp.json()["job_id"]
    missing = jobs_client.get(f"/v1/jobs/{job_id}", headers=_headers("proj-b"))
    assert missing.status_code == 404
    assert missing.json()["code"] == "job_not_found"


def test_missing_project_header_400(jobs_client: TestClient) -> None:
    resp = jobs_client.post(
        "/v1/jobs",
        json={"model": "local-general", "task": "generate", "input": "x"},
    )
    assert resp.status_code == 400
    assert resp.json()["code"] == "project_required"


def test_concurrency_cap_respected(
    env_valid: None, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    config = tmp_path / "models.yaml"
    config.write_text(
        yaml.safe_dump(
            {
                "models": [
                    {
                        "id": "local-general",
                        "backend": "fake",
                        "capabilities": ["generate", "classify", "summarize"],
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("FORGE_MODELS_CONFIG", str(config))
    monkeypatch.setenv("FORGE_MODELS_MAX_CONCURRENT_JOBS", "1")
    monkeypatch.setenv("FORGE_MODELS_JOB_TIMEOUT_SECONDS", "5")
    clear_settings_cache()
    application = create_app()
    with TestClient(application) as client:
        ids = []
        for _ in range(2):
            resp = client.post(
                "/v1/jobs",
                headers=_headers(),
                json={
                    "model": "local-general",
                    "task": "summarize",
                    "input": "cap",
                    "delay_ms": 400,
                },
            )
            assert resp.status_code == 202
            ids.append(resp.json()["job_id"])

        # Briefly observe: at most one running while the other is queued.
        saw_queued_while_running = False
        deadline = time.time() + 1.5
        while time.time() < deadline:
            statuses = [
                client.get(f"/v1/jobs/{jid}", headers=_headers()).json()["status"] for jid in ids
            ]
            if statuses.count("running") == 1 and statuses.count("queued") == 1:
                saw_queued_while_running = True
                break
            if all(s in {"succeeded", "failed", "cancelled"} for s in statuses):
                break
            time.sleep(0.02)
        assert saw_queued_while_running
        for jid in ids:
            final = _poll_terminal(client, jid)
            assert final["status"] == "succeeded"
    clear_settings_cache()
