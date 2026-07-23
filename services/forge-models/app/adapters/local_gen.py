"""Local generation adapter: deterministic generate/classify/summarize for CI."""

from __future__ import annotations

import math
import re
from dataclasses import dataclass
from typing import FrozenSet, Iterable, Sequence

from app.adapters.base import Capability, HealthStatus, ModelAdapter
from app.adapters.local_embed import deterministic_embed

_SUMMARIZE_PREFIX = "Summarize the following text:\n\n"
_CLASSIFY_EMBED_DIM = 64
_WORD_RE = re.compile(r"\S+")


@dataclass(frozen=True)
class TokenUsage:
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int

    def as_dict(self) -> dict[str, int]:
        return {
            "prompt_tokens": self.prompt_tokens,
            "completion_tokens": self.completion_tokens,
            "total_tokens": self.total_tokens,
        }


@dataclass(frozen=True)
class GenerateResult:
    text: str
    finish_reason: str
    usage: TokenUsage


@dataclass(frozen=True)
class LabelScore:
    label: str
    score: float

    def as_dict(self) -> dict[str, float | str]:
        return {"label": self.label, "score": self.score}


def approximate_tokens(text: str) -> int:
    """Approximate token count via whitespace-separated words (min 0)."""
    if not text:
        return 0
    return max(1, len(_WORD_RE.findall(text))) if text.strip() else 0


def summarize_prompt(text: str) -> str:
    """Build the summarization prompt consumed by ``generate``."""
    return f"{_SUMMARIZE_PREFIX}{text}"


def _cosine(a: Sequence[float], b: Sequence[float]) -> float:
    return sum(x * y for x, y in zip(a, b, strict=True))


def _truncate_words(text: str, max_tokens: int) -> tuple[str, str]:
    words = _WORD_RE.findall(text)
    if not words:
        return "", "stop"
    if len(words) <= max_tokens:
        return " ".join(words), "stop"
    return " ".join(words[:max_tokens]), "length"


class LocalGenerationAdapter(ModelAdapter):
    """Deterministic fake generation/classification; optional real model later."""

    def __init__(
        self,
        *,
        model_id: str,
        backend: str,
        capabilities: Iterable[Capability],
        health_status: HealthStatus = HealthStatus.OK,
    ) -> None:
        caps = frozenset(capabilities)
        gen_caps = {Capability.GENERATE, Capability.CLASSIFY, Capability.SUMMARIZE}
        if not (caps & gen_caps):
            raise ValueError(
                f"LocalGenerationAdapter requires generate/classify/summarize (model={model_id})"
            )
        self._model_id = model_id
        self._backend = backend
        self._capabilities = caps
        self._health = health_status

    @property
    def model_id(self) -> str:
        return self._model_id

    @property
    def backend(self) -> str:
        return self._backend

    @property
    def capabilities(self) -> FrozenSet[Capability]:
        return self._capabilities

    @property
    def embedding_dim(self) -> int | None:
        return None

    def health(self) -> HealthStatus:
        return self._health

    def generate(
        self,
        prompt: str,
        *,
        max_tokens: int,
        temperature: float,
    ) -> GenerateResult:
        """Deterministic generate. ``temperature=0`` is fully stable in CI."""
        if max_tokens < 1:
            raise ValueError("max_tokens must be >= 1")
        if temperature < 0.0:
            raise ValueError("temperature must be >= 0")

        if prompt.startswith(_SUMMARIZE_PREFIX):
            source = prompt[len(_SUMMARIZE_PREFIX) :]
            text, finish_reason = self._summarize_text(source, max_tokens=max_tokens)
        else:
            text, finish_reason = self._generate_text(
                prompt, max_tokens=max_tokens, temperature=temperature
            )

        usage = TokenUsage(
            prompt_tokens=approximate_tokens(prompt),
            completion_tokens=approximate_tokens(text),
            total_tokens=approximate_tokens(prompt) + approximate_tokens(text),
        )
        return GenerateResult(text=text, finish_reason=finish_reason, usage=usage)

    def classify(self, text: str, labels: Sequence[str]) -> list[LabelScore]:
        """Score labels by cosine similarity of deterministic embeddings."""
        if not labels:
            raise ValueError("labels must be non-empty")
        input_vec = deterministic_embed(text, _CLASSIFY_EMBED_DIM)
        scored: list[LabelScore] = []
        for label in labels:
            label_vec = deterministic_embed(label, _CLASSIFY_EMBED_DIM)
            raw = _cosine(input_vec, label_vec)
            # Map cosine [-1, 1] → [0, 1] for stable non-negative scores.
            score = (raw + 1.0) / 2.0
            # Exact label match always ranks first (score 1.0).
            if label == text:
                score = 1.0
            scored.append(LabelScore(label=label, score=score))
        scored.sort(key=lambda item: (-item.score, item.label))
        return scored

    def _generate_text(
        self, prompt: str, *, max_tokens: int, temperature: float
    ) -> tuple[str, str]:
        # Deterministic transform: prefix + normalized whitespace.
        # Non-zero temperature only reshuffles a seeded suffix marker; body stays stable.
        normalized = " ".join(_WORD_RE.findall(prompt))
        body = f"[forge-gen] {normalized}".strip() if normalized else "[forge-gen]"
        if temperature > 0.0:
            # Keep deterministic: include quantized temperature in a marker only.
            marker = f" t={temperature:.4f}"
            body = f"{body}{marker}"
        return _truncate_words(body, max_tokens)

    def _summarize_text(self, source: str, *, max_tokens: int) -> tuple[str, str]:
        words = _WORD_RE.findall(source)
        if not words:
            return "[forge-summary]", "stop"
        # Target ~1/4 of input length, at least 1 word, capped by max_tokens.
        target = min(max_tokens, max(1, math.ceil(len(words) / 4)))
        summary = " ".join(words[:target])
        finish = "stop" if target >= len(words) or target < max_tokens else "length"
        if len(words) > target:
            # Explicit shorter-than-input summary for long inputs.
            return f"[forge-summary] {summary}", "stop"
        return f"[forge-summary] {summary}", finish
