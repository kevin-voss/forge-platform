"""Unit tests for LocalGenerationAdapter determinism and classify scoring."""

from __future__ import annotations

from app.adapters.base import Capability
from app.adapters.local_gen import LocalGenerationAdapter, summarize_prompt


def _adapter() -> LocalGenerationAdapter:
    return LocalGenerationAdapter(
        model_id="local-general",
        backend="fake",
        capabilities=[Capability.GENERATE, Capability.CLASSIFY, Capability.SUMMARIZE],
    )


def test_generate_temperature_zero_deterministic() -> None:
    adapter = _adapter()
    a = adapter.generate("hello forge platform", max_tokens=32, temperature=0.0)
    b = adapter.generate("hello forge platform", max_tokens=32, temperature=0.0)
    assert a.text == b.text
    assert a.finish_reason == b.finish_reason
    assert a.usage == b.usage
    assert a.text.startswith("[forge-gen]")
    assert a.usage.total_tokens == a.usage.prompt_tokens + a.usage.completion_tokens


def test_classify_sorted_and_identical_label_highest() -> None:
    adapter = _adapter()
    scored = adapter.classify("network", ["auth", "network", "disk"])
    assert scored[0].label == "network"
    assert scored[0].score == 1.0
    assert scored[0].score >= scored[1].score >= scored[2].score
    assert all(0.0 <= item.score <= 1.0 for item in scored)
    assert {item.label for item in scored} == {"auth", "network", "disk"}


def test_summarize_shorter_than_long_input() -> None:
    adapter = _adapter()
    words = [f"word{i}" for i in range(40)]
    long_input = " ".join(words)
    prompt = summarize_prompt(long_input)
    result = adapter.generate(prompt, max_tokens=128, temperature=0.0)
    assert result.text
    assert len(result.text.split()) < len(words)
    assert "[forge-summary]" in result.text
