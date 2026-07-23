"""Unit tests for LocalEmbeddingAdapter determinism and dimensions."""

from __future__ import annotations

import math

from app.adapters.base import Capability
from app.adapters.local_embed import LocalEmbeddingAdapter, deterministic_embed, l2_normalize


def _cosine(a: list[float], b: list[float]) -> float:
    return sum(x * y for x, y in zip(a, b, strict=True))


def test_deterministic_embed_same_input() -> None:
    a = deterministic_embed("hello forge", 384)
    b = deterministic_embed("hello forge", 384)
    assert a == b
    assert len(a) == 384
    norm = math.sqrt(sum(v * v for v in a))
    assert abs(norm - 1.0) < 1e-9


def test_distinct_inputs_distinct_vectors() -> None:
    a = deterministic_embed("alpha", 64)
    b = deterministic_embed("beta", 64)
    assert a != b
    assert _cosine(a, a) == 1.0 or abs(_cosine(a, a) - 1.0) < 1e-9


def test_l2_normalize_unit() -> None:
    vec = l2_normalize([3.0, 4.0])
    assert abs(vec[0] - 0.6) < 1e-9
    assert abs(vec[1] - 0.8) < 1e-9


def test_adapter_embed_matches_dim_and_determinism() -> None:
    adapter = LocalEmbeddingAdapter(
        model_id="local-embed-small",
        backend="local",
        capabilities=[Capability.EMBED],
        embedding_dim=384,
    )
    adapter.assert_dimension()
    a = adapter.embed(["same text"])[0]
    b = adapter.embed(["same text"])[0]
    assert a == b
    assert len(a) == 384
    assert adapter.embed_mode == "deterministic"
    assert abs(_cosine(a, b) - 1.0) < 1e-9


def test_adapter_batch_shape() -> None:
    adapter = LocalEmbeddingAdapter(
        model_id="local-embed-small",
        backend="fake",
        capabilities=[Capability.EMBED],
        embedding_dim=32,
    )
    out = adapter.embed(["one", "two", "three"])
    assert len(out) == 3
    assert all(len(v) == 32 for v in out)
    assert out[0] != out[1]
