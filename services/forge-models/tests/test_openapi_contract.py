"""Contract tests for forge-models OpenAPI (registry schemas)."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml
from fastapi.testclient import TestClient

ALLOWED_CAPS = {"embed", "generate", "classify", "summarize"}
ALLOWED_STATUS = {"ok", "degraded", "down"}


def _repo_openapi() -> Path:
    """Resolve canonical OpenAPI when the full repo tree is present."""
    here = Path(__file__).resolve()
    if len(here.parents) > 3:
        candidate = here.parents[3] / "contracts" / "openapi" / "forge-models.openapi.yaml"
        if candidate.is_file():
            return candidate
    for parent in here.parents:
        candidate = parent / "contracts" / "openapi" / "forge-models.openapi.yaml"
        if candidate.is_file():
            return candidate
    return (
        here.parents[min(3, len(here.parents) - 1)]
        / "contracts"
        / "openapi"
        / "forge-models.openapi.yaml"
    )


OPENAPI = _repo_openapi()


def test_openapi_documents_model_list_get_health() -> None:
    if not OPENAPI.is_file():
        pytest.skip(f"canonical OpenAPI not in build context ({OPENAPI})")
    doc = yaml.safe_load(OPENAPI.read_text(encoding="utf-8"))
    paths = doc["paths"]
    assert "/v1/models" in paths
    assert "/v1/models/{model}" in paths
    assert "/v1/models/{model}/health" in paths
    assert "/v1/models/{model}/embed" in paths
    assert "/v1/models/{model}/generate" in paths
    assert "/v1/models/{model}/classify" in paths
    assert "/v1/models/{model}/summarize" in paths
    assert "/v1/jobs" in paths
    assert "/v1/jobs/{job_id}" in paths
    for path_key in (
        "/v1/models/{model}/embed",
        "/v1/models/{model}/generate",
        "/v1/models/{model}/classify",
        "/v1/models/{model}/summarize",
    ):
        op = paths[path_key]["post"]
        assert "200" in op["responses"]
        assert "404" in op["responses"]
        assert "422" in op["responses"]
    gen_op = paths["/v1/models/{model}/generate"]["post"]
    stream_param = next(p for p in gen_op["parameters"] if p.get("name") == "stream")
    assert stream_param["in"] == "query"
    assert "text/event-stream" in gen_op["responses"]["200"]["content"]
    jobs_post = paths["/v1/jobs"]["post"]
    assert "202" in jobs_post["responses"]
    jobs_get = paths["/v1/jobs/{job_id}"]["get"]
    assert "200" in jobs_get["responses"]
    assert "404" in jobs_get["responses"]
    jobs_del = paths["/v1/jobs/{job_id}"]["delete"]
    assert "200" in jobs_del["responses"]
    assert "409" in jobs_del["responses"]
    schemas = doc["components"]["schemas"]
    assert "Model" in schemas
    assert "ModelListResponse" in schemas
    assert "ModelHealth" in schemas
    assert "ErrorBody" in schemas
    assert "EmbedRequest" in schemas
    assert "EmbedResponse" in schemas
    assert "EmbedUsage" in schemas
    assert "TokenUsage" in schemas
    assert "GenerateRequest" in schemas
    assert "GenerateResponse" in schemas
    assert "ClassifyRequest" in schemas
    assert "ClassifyResponse" in schemas
    assert "SummarizeRequest" in schemas
    assert "SummarizeResponse" in schemas
    assert "CreateJobRequest" in schemas
    assert "CreateJobResponse" in schemas
    assert "JobStatusResponse" in schemas
    assert "CancelJobResponse" in schemas
    assert "JobStatus" in schemas
    assert "JobTask" in schemas
    embed_resp = schemas["EmbedResponse"]
    assert set(embed_resp["required"]) >= {"model", "embeddings", "dim", "usage"}
    assert "dim" in embed_resp["properties"]
    assert "usage" in embed_resp["properties"]
    usage = schemas["TokenUsage"]
    assert set(usage["required"]) >= {"prompt_tokens", "completion_tokens", "total_tokens"}
    gen_resp = schemas["GenerateResponse"]
    assert set(gen_resp["required"]) >= {"text", "finish_reason", "usage"}
    classify_resp = schemas["ClassifyResponse"]
    assert set(classify_resp["required"]) >= {"labels"}
    summarize_resp = schemas["SummarizeResponse"]
    assert set(summarize_resp["required"]) >= {"summary", "usage"}
    job_resp = schemas["JobStatusResponse"]
    assert set(job_resp["required"]) >= {"status"}
    assert "result" in job_resp["properties"]
    assert "error" in job_resp["properties"]
    create_resp = schemas["CreateJobResponse"]
    assert set(create_resp["required"]) >= {"job_id", "status"}
    caps = schemas["ModelCapability"]["enum"]
    assert set(caps) == ALLOWED_CAPS
    assert schemas["Model"]["properties"]["embedding_dim"].get("nullable") is True
    assert set(schemas["JobTask"]["enum"]) == {"generate", "classify", "summarize", "embed"}
    assert set(schemas["JobStatus"]["enum"]) == {
        "queued",
        "running",
        "succeeded",
        "failed",
        "cancelled",
    }


def _assert_embed_shape(body: dict) -> None:
    assert set(body) >= {"model", "embeddings", "dim", "usage"}
    assert isinstance(body["model"], str) and body["model"]
    assert isinstance(body["dim"], int) and body["dim"] >= 1
    assert isinstance(body["embeddings"], list) and body["embeddings"]
    assert isinstance(body["usage"], dict)
    assert body["usage"].get("input_count") == len(body["embeddings"])
    for vec in body["embeddings"]:
        assert isinstance(vec, list)
        assert len(vec) == body["dim"]
        assert all(isinstance(x, (int, float)) for x in vec)


def _assert_token_usage(usage: dict) -> None:
    assert set(usage) >= {"prompt_tokens", "completion_tokens", "total_tokens"}
    assert isinstance(usage["prompt_tokens"], int) and usage["prompt_tokens"] >= 0
    assert isinstance(usage["completion_tokens"], int) and usage["completion_tokens"] >= 0
    assert usage["total_tokens"] == usage["prompt_tokens"] + usage["completion_tokens"]


def _assert_generate_shape(body: dict) -> None:
    assert set(body) >= {"text", "finish_reason", "usage"}
    assert isinstance(body["text"], str) and body["text"]
    assert body["finish_reason"] in {"stop", "length"}
    _assert_token_usage(body["usage"])


def _assert_classify_shape(body: dict) -> None:
    assert isinstance(body.get("labels"), list) and body["labels"]
    scores = []
    for item in body["labels"]:
        assert isinstance(item.get("label"), str) and item["label"]
        assert isinstance(item.get("score"), (int, float))
        scores.append(item["score"])
    assert scores == sorted(scores, reverse=True)


def _assert_summarize_shape(body: dict) -> None:
    assert isinstance(body.get("summary"), str) and body["summary"]
    _assert_token_usage(body["usage"])


def _assert_model_shape(model: dict) -> None:
    assert set(model) >= {"id", "capabilities", "backend", "embedding_dim", "status"}
    assert isinstance(model["id"], str) and model["id"]
    assert isinstance(model["backend"], str) and model["backend"]
    assert isinstance(model["capabilities"], list)
    assert set(model["capabilities"]).issubset(ALLOWED_CAPS)
    assert model["status"] in ALLOWED_STATUS
    dim = model["embedding_dim"]
    assert dim is None or (isinstance(dim, int) and dim >= 1)


def test_list_get_health_responses_validate(client: TestClient) -> None:
    listed = client.get("/v1/models")
    assert listed.status_code == 200
    body = listed.json()
    assert isinstance(body.get("models"), list)
    assert body["models"]
    for model in body["models"]:
        _assert_model_shape(model)

    sample_id = body["models"][0]["id"]
    got = client.get(f"/v1/models/{sample_id}")
    assert got.status_code == 200
    _assert_model_shape(got.json())

    health = client.get(f"/v1/models/{sample_id}/health")
    assert health.status_code == 200
    assert health.json()["status"] in ALLOWED_STATUS

    missing = client.get("/v1/models/no-such-model")
    assert missing.status_code == 404
    err = missing.json()
    assert err["code"] == "model_not_found"
    assert isinstance(err["error"], str)

    embed_model = next(m["id"] for m in body["models"] if "embed" in m["capabilities"])
    embedded = client.post(f"/v1/models/{embed_model}/embed", json={"input": "contract"})
    assert embedded.status_code == 200
    _assert_embed_shape(embedded.json())

    non_embed = next(
        (m["id"] for m in body["models"] if "embed" not in m["capabilities"]),
        None,
    )
    if non_embed is not None:
        unsupported = client.post(f"/v1/models/{non_embed}/embed", json={"input": "x"})
        assert unsupported.status_code == 422
        assert unsupported.json()["code"] == "capability_unsupported"

    gen_model = next(
        (m["id"] for m in body["models"] if "generate" in m["capabilities"]),
        None,
    )
    if gen_model is not None:
        generated = client.post(
            f"/v1/models/{gen_model}/generate",
            json={"prompt": "contract", "max_tokens": 16, "temperature": 0},
        )
        assert generated.status_code == 200
        _assert_generate_shape(generated.json())

        classified = client.post(
            f"/v1/models/{gen_model}/classify",
            json={"input": "contract", "labels": ["a", "b"]},
        )
        assert classified.status_code == 200
        _assert_classify_shape(classified.json())

        summarized = client.post(
            f"/v1/models/{gen_model}/summarize",
            json={"input": "contract text for summary checks"},
        )
        assert summarized.status_code == 200
        _assert_summarize_shape(summarized.json())

        with client.stream(
            "POST",
            f"/v1/models/{gen_model}/generate?stream=true",
            json={"prompt": "contract stream", "max_tokens": 16, "temperature": 0},
        ) as streamed:
            assert streamed.status_code == 200
            assert "text/event-stream" in streamed.headers.get("content-type", "")
            stream_body = "".join(streamed.iter_text())
        assert "data: [DONE]" in stream_body

        job = client.post(
            "/v1/jobs",
            headers={"X-Forge-Project": "contract-proj"},
            json={"model": gen_model, "task": "summarize", "input": "contract job"},
        )
        assert job.status_code == 202
        job_body = job.json()
        assert job_body["status"] == "queued"
        assert isinstance(job_body.get("job_id"), str) and job_body["job_id"]
        # Poll briefly for a valid status shape.
        status = client.get(
            f"/v1/jobs/{job_body['job_id']}",
            headers={"X-Forge-Project": "contract-proj"},
        )
        assert status.status_code == 200
        assert status.json()["status"] in {
            "queued",
            "running",
            "succeeded",
            "failed",
            "cancelled",
        }

    embed_only = next(
        (
            m["id"]
            for m in body["models"]
            if "embed" in m["capabilities"] and "generate" not in m["capabilities"]
        ),
        None,
    )
    if embed_only is not None:
        mismatch = client.post(
            f"/v1/models/{embed_only}/generate",
            json={"prompt": "x", "temperature": 0},
        )
        assert mismatch.status_code == 422
        assert mismatch.json()["code"] == "capability_unsupported"


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
