"""Unit + integration tests for SSE generate streaming."""

from __future__ import annotations

import asyncio
import json

from fastapi.testclient import TestClient

from app.adapters.base import Capability
from app.adapters.local_gen import LocalGenerationAdapter
from app.streaming import DONE_SENTINEL, chunk_text, format_sse_data, iter_generate_deltas


def test_format_sse_data_delta_and_done() -> None:
    assert format_sse_data({"delta": "hi"}) == 'data: {"delta":"hi"}\n\n'
    assert format_sse_data(DONE_SENTINEL) == "data: [DONE]\n\n"


def test_chunk_text_emits_word_deltas() -> None:
    assert chunk_text("one two three") == ["one", " two", " three"]


def test_iter_generate_deltas_then_exhausted() -> None:
    adapter = LocalGenerationAdapter(
        model_id="local-general",
        backend="fake",
        capabilities=[Capability.GENERATE, Capability.CLASSIFY, Capability.SUMMARIZE],
    )

    async def _collect() -> list[str]:
        chunks: list[str] = []
        async for delta in iter_generate_deltas(
            adapter, "stream please now", max_tokens=16, temperature=0.0
        ):
            chunks.append(delta)
        return chunks

    chunks = asyncio.run(_collect())
    assert len(chunks) >= 2
    assert "".join(chunks).startswith("[forge-gen]")


def test_generate_stream_emits_chunks_and_done(client: TestClient) -> None:
    with client.stream(
        "POST",
        "/v1/models/local-general/generate?stream=true",
        json={"prompt": "stream please now extra words", "max_tokens": 32, "temperature": 0},
    ) as resp:
        assert resp.status_code == 200
        assert "text/event-stream" in resp.headers.get("content-type", "")
        body = "".join(resp.iter_text())

    events = [line[6:] for line in body.splitlines() if line.startswith("data: ")]
    assert events, body
    assert events[-1] == DONE_SENTINEL
    deltas = [json.loads(e)["delta"] for e in events[:-1] if e.startswith("{")]
    assert len(deltas) >= 2
    assert "".join(deltas).startswith("[forge-gen]")


def test_generate_stream_false_still_json(client: TestClient) -> None:
    resp = client.post(
        "/v1/models/local-general/generate?stream=false",
        json={"prompt": "hello", "max_tokens": 16, "temperature": 0},
    )
    assert resp.status_code == 200
    assert "text" in resp.json()
