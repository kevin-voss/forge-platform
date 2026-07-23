"""Unit/integration tests for usage metrics, /metrics, /v1/usage, and redaction."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from app.metrics import UsageMetrics, redact_for_log


def test_redact_truncates_oversized_strings() -> None:
    long = "x" * 500
    redacted = redact_for_log(long, max_chars=32)
    assert isinstance(redacted, str)
    assert redacted.startswith("x" * 32)
    assert "500 chars" in redacted
    assert len(redacted) < len(long)


def test_redact_collapses_large_lists() -> None:
    assert redact_for_log(["a" * 400, "b" * 400]) == "<list len=2>"


def test_metric_counters_increment_per_capability(client: TestClient) -> None:
    embed = client.post(
        "/v1/models/local-embed-small/embed",
        json={"input": "metrics-embed"},
    )
    assert embed.status_code == 200

    gen = client.post(
        "/v1/models/local-general/generate",
        json={"prompt": "metrics-gen", "max_tokens": 16, "temperature": 0},
    )
    assert gen.status_code == 200

    metrics = client.get("/metrics")
    assert metrics.status_code == 200
    body = metrics.text
    assert "models_embed_requests_total" in body
    assert 'model="local-embed-small"' in body
    assert "models_generate_requests_total" in body
    assert 'model="local-general"' in body
    assert "models_latency_seconds" in body
    assert 'capability="embed"' in body or 'capability="generate"' in body


def test_usage_snapshot_aggregates(client: TestClient) -> None:
    before = client.get("/v1/usage")
    assert before.status_code == 200
    assert "by_model" in before.json()

    client.post("/v1/models/local-embed-small/embed", json={"input": "usage-a"})
    client.post("/v1/models/local-embed-small/embed", json={"input": "usage-b"})
    client.post(
        "/v1/models/local-general/summarize",
        json={"input": "usage summarize please"},
    )
    # Force an error increment.
    client.post("/v1/models/nope/embed", json={"input": "x"})

    usage = client.get("/v1/usage").json()
    by_model = usage["by_model"]
    assert "local-embed-small" in by_model
    embed_stats = by_model["local-embed-small"]
    assert embed_stats["requests"] >= 2
    assert embed_stats["tokens"] >= 2
    assert "p95_latency_ms" in embed_stats
    assert embed_stats["p95_latency_ms"] >= 0

    assert "local-general" in by_model
    assert by_model["local-general"]["requests"] >= 1

    assert "nope" in by_model
    assert by_model["nope"]["errors"] >= 1


def test_metrics_recording_failure_does_not_break_inference(client: TestClient) -> None:
    usage: UsageMetrics = client.app.state.usage_metrics

    def boom(**_kwargs: object) -> None:
        raise RuntimeError("metrics exploded")

    original = usage.record
    usage.record = boom  # type: ignore[method-assign]
    try:
        response = client.post(
            "/v1/models/local-embed-small/embed",
            json={"input": "still-works"},
        )
        assert response.status_code == 200
        assert response.json()["dim"] == 384
    finally:
        usage.record = original  # type: ignore[method-assign]


def test_metrics_disabled_exports_empty(clean_env: pytest.MonkeyPatch) -> None:
    from app.config import clear_settings_cache
    from app.main import create_app

    clean_env.setenv("PORT", "4300")
    clean_env.setenv("FORGE_MODELS_BACKEND", "fake")
    clean_env.setenv("FORGE_MODELS_METRICS_ENABLED", "false")
    clean_env.setenv("FORGE_LOG_LEVEL", "error")
    clear_settings_cache()
    with TestClient(create_app()) as client:
        client.post("/v1/models/local-embed-small/embed", json={"input": "x"})
        metrics = client.get("/metrics")
        assert metrics.status_code == 200
        assert "metrics disabled" in metrics.text
        usage = client.get("/v1/usage").json()
        assert usage == {"by_model": {}}
    clear_settings_cache()
