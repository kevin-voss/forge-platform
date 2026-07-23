"""Identity JSON unit tests."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_identity_required_fields(client: TestClient) -> None:
    resp = client.get("/")
    assert resp.status_code == 200
    body = resp.json()
    assert body["service"] == "forge-agents"
    assert body["language"] == "python"
    assert body["status"] == "running"
    assert body["version"] == "0.1.0"
    assert "uptime_seconds" in body
    assert isinstance(body["uptime_seconds"], (int, float))
