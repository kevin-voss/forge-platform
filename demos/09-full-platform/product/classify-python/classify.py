"""Deterministic incident classification (stable labels for later assertions)."""

from __future__ import annotations

import hashlib
import re
from dataclasses import dataclass


# Keyword rules win over hash fallback so product demos stay readable.
_KEYWORD_LABELS: tuple[tuple[str, str], ...] = (
    (r"\b(oom|out of memory|memory leak)\b", "resource_exhaustion"),
    (r"\b(timeout|latency|slow|p99)\b", "performance"),
    (r"\b(deploy|rollback|release|canary)\b", "deployment"),
    (r"\b(auth|unauthorized|forbidden|token)\b", "security"),
    (r"\b(disk|filesystem|inode|volume)\b", "storage"),
    (r"\b(network|dns|connect|refused)\b", "connectivity"),
)

_FALLBACK_LABELS = (
    "unknown",
    "infrastructure",
    "application",
    "configuration",
)


@dataclass(frozen=True)
class Classification:
    label: str
    confidence: float
    reason: str


def classify_text(text: str) -> Classification:
    """Return a stable label for the same input text."""
    normalized = " ".join(text.lower().split())
    for pattern, label in _KEYWORD_LABELS:
        if re.search(pattern, normalized):
            return Classification(label=label, confidence=0.92, reason="keyword_match")

    digest = hashlib.sha256(normalized.encode("utf-8")).hexdigest()
    idx = int(digest[:8], 16) % len(_FALLBACK_LABELS)
    return Classification(
        label=_FALLBACK_LABELS[idx],
        confidence=0.55,
        reason="stable_hash",
    )
