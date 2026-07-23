"""Health route unit/integration tests via TestClient."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_live_returns_live(client: TestClient) -> None:
    resp = client.get("/health/live")
    assert resp.status_code == 200
    assert resp.json() == {"status": "live"}


def test_ready_returns_ready(client: TestClient) -> None:
    resp = client.get("/health/ready")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ready"}


def test_request_id_header_echoed(client: TestClient) -> None:
    resp = client.get("/health/live", headers={"X-Request-ID": "req-test-1"})
    assert resp.status_code == 200
    assert resp.headers.get("X-Request-ID") == "req-test-1"


def test_request_id_minted_when_absent(client: TestClient) -> None:
    resp = client.get("/health/live")
    assert resp.status_code == 200
    assert resp.headers.get("X-Request-ID")
