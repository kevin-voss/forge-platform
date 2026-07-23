"""Contract tests for forge-models OpenAPI skeleton."""

from __future__ import annotations

from pathlib import Path

import pytest
from fastapi.testclient import TestClient

def _repo_openapi() -> Path:
    """Resolve canonical OpenAPI when the full repo tree is present."""
    here = Path(__file__).resolve()
    # Local checkout: services/forge-models/tests → repo root is parents[3]
    if len(here.parents) > 3:
        candidate = here.parents[3] / "contracts" / "openapi" / "forge-models.openapi.yaml"
        if candidate.is_file():
            return candidate
    # Walk up looking for contracts/openapi/...
    for parent in here.parents:
        candidate = parent / "contracts" / "openapi" / "forge-models.openapi.yaml"
        if candidate.is_file():
            return candidate
    return here.parents[min(3, len(here.parents) - 1)] / "contracts" / "openapi" / "forge-models.openapi.yaml"


OPENAPI = _repo_openapi()


def test_openapi_file_exists_and_declares_three_paths() -> None:
    if not OPENAPI.is_file():
        pytest.skip(f"canonical OpenAPI not in build context ({OPENAPI})")
    text = OPENAPI.read_text(encoding="utf-8")
    assert "openapi:" in text
    assert "/health/live" in text
    assert "/health/ready" in text
    assert "\n  /:" in text
    assert "forge-models" in text
    # No inference surface yet (14.02+)
    assert "/v1/models" not in text


def test_live_ready_identity_match_contract(client: TestClient) -> None:
    live = client.get("/health/live")
    ready = client.get("/health/ready")
    identity = client.get("/")
    assert live.status_code == 200 and live.json() == {"status": "live"}
    assert ready.status_code == 200 and ready.json() == {"status": "ready"}
    body = identity.json()
    for key in ("service", "language", "status", "version"):
        assert key in body


def test_invalid_backend_fails_create_app(clean_env: pytest.MonkeyPatch) -> None:
    from pydantic import ValidationError

    from app.main import create_app

    clean_env.setenv("PORT", "4300")
    clean_env.setenv("FORGE_MODELS_BACKEND", "bogus")
    with pytest.raises(ValidationError):
        create_app()
