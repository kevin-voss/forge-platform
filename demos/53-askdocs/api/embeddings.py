"""Pinned Models↔Memory embedding contract for AskDocs (epic 53.03).

Forge Models `local-embed-small` (fake/deterministic backend) produces
L2-normalized float vectors of dimension 384. Forge Memory collections must use
the same dim + cosine distance. A mismatch is a Models↔Memory contract finding.
"""

from __future__ import annotations

import math
from typing import Sequence

# Pinned contract — do not diverge from forge-models registry / demo 17 fixtures.
EMBEDDING_MODEL = "local-embed-small"
EMBEDDING_DIM = 384
MEMORY_COLLECTION = "askdocs-chunks"
MEMORY_DISTANCE = "cosine"


class EmbeddingContractError(ValueError):
    """Raised when an embedding violates the pinned Models↔Memory contract."""


def assert_embedding_contract(vector: Sequence[float], *, dim: int | None = None) -> list[float]:
    """Validate length and finite floats; return a concrete list copy."""
    expected = EMBEDDING_DIM if dim is None else dim
    if not isinstance(vector, (list, tuple)):
        raise EmbeddingContractError(f"embedding must be a list, got {type(vector).__name__}")
    if len(vector) != expected:
        raise EmbeddingContractError(
            f"embedding dim mismatch: got {len(vector)}, expected {expected} "
            f"(model={EMBEDDING_MODEL}, collection={MEMORY_COLLECTION})"
        )
    out: list[float] = []
    for i, v in enumerate(vector):
        try:
            f = float(v)
        except (TypeError, ValueError) as exc:
            raise EmbeddingContractError(f"embedding[{i}] is not a float: {v!r}") from exc
        if not math.isfinite(f):
            raise EmbeddingContractError(f"embedding[{i}] is not finite: {f}")
        out.append(f)
    return out


def l2_norm(vector: Sequence[float]) -> float:
    return math.sqrt(sum(float(v) * float(v) for v in vector))
