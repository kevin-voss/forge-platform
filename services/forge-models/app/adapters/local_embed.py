"""Local embeddings adapter: deterministic CI backend + optional on-disk model."""

from __future__ import annotations

import hashlib
import logging
import math
import random
from pathlib import Path
from typing import FrozenSet, Iterable, Sequence

from app.adapters.base import Capability, HealthStatus, ModelAdapter

logger = logging.getLogger("forge-models")


def l2_normalize(vector: Sequence[float]) -> list[float]:
    """Return an L2-normalized copy of ``vector`` (zero vector → zeros)."""
    norm = math.sqrt(sum(v * v for v in vector))
    if norm == 0.0:
        return [0.0 for _ in vector]
    return [v / norm for v in vector]


def deterministic_embed(text: str, dim: int) -> list[float]:
    """Hash ``text`` into a seeded pseudo-random L2-normalized vector of length ``dim``."""
    digest = hashlib.sha256(text.encode("utf-8")).digest()
    seed = int.from_bytes(digest[:8], byteorder="big", signed=False)
    rng = random.Random(seed)
    raw = [rng.gauss(0.0, 1.0) for _ in range(dim)]
    return l2_normalize(raw)


class LocalEmbeddingAdapter(ModelAdapter):
    """Embeddings via deterministic hashing, or a local sentence-transformer when configured."""

    def __init__(
        self,
        *,
        model_id: str,
        backend: str,
        capabilities: Iterable[Capability],
        embedding_dim: int,
        local_model_path: str | None = None,
        health_status: HealthStatus = HealthStatus.OK,
    ) -> None:
        caps = frozenset(capabilities)
        if Capability.EMBED not in caps:
            raise ValueError(f"LocalEmbeddingAdapter requires embed capability (model={model_id})")
        if embedding_dim < 1:
            raise ValueError(f"embedding_dim must be >= 1 (model={model_id})")

        self._model_id = model_id
        self._backend = backend
        self._capabilities = caps
        self._embedding_dim = embedding_dim
        self._health = health_status
        self._local_model_path = (local_model_path or "").strip() or None
        self._transformer = None
        self._embed_mode = "deterministic"

        if self._local_model_path:
            self._try_load_transformer(self._local_model_path)

    def _try_load_transformer(self, path: str) -> None:
        resolved = Path(path).expanduser()
        if not resolved.exists():
            raise ValueError(
                f"FORGE_MODELS_LOCAL_MODEL_PATH does not exist: {resolved} (model={self._model_id})"
            )
        try:
            from sentence_transformers import SentenceTransformer  # type: ignore[import-not-found]
        except ImportError as exc:
            raise ValueError(
                "FORGE_MODELS_LOCAL_MODEL_PATH is set but sentence-transformers is not installed; "
                "omit the path to use the deterministic CI embedder"
            ) from exc

        model = SentenceTransformer(str(resolved))
        probe = model.encode(["__forge_dim_probe__"], normalize_embeddings=True)
        produced = (
            int(getattr(probe, "shape", [0, 0])[1]) if hasattr(probe, "shape") else len(probe[0])
        )
        if produced != self._embedding_dim:
            raise ValueError(
                f"local model at {resolved} produced dim={produced}, "
                f"registry embedding_dim={self._embedding_dim} (model={self._model_id})"
            )
        self._transformer = model
        self._embed_mode = "transformer"
        logger.info(
            "loaded local embedding model",
            extra={"model": self._model_id, "path": str(resolved), "dim": self._embedding_dim},
        )

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
    def embedding_dim(self) -> int:
        return self._embedding_dim

    @property
    def embed_mode(self) -> str:
        """``deterministic`` (CI) or ``transformer`` (optional local model)."""
        return self._embed_mode

    def health(self) -> HealthStatus:
        return self._health

    def embed(self, texts: Sequence[str]) -> list[list[float]]:
        """Embed texts into fixed-dimension L2-normalized vectors."""
        if not texts:
            return []
        if self._transformer is not None:
            encoded = self._transformer.encode(list(texts), normalize_embeddings=True)
            vectors: list[list[float]] = []
            for row in encoded:
                vec = [float(x) for x in row]
                if len(vec) != self._embedding_dim:
                    raise RuntimeError(
                        f"embedding dim mismatch: got {len(vec)}, "
                        f"expected {self._embedding_dim} (model={self._model_id})"
                    )
                vectors.append(l2_normalize(vec))
            return vectors

        return [deterministic_embed(text, self._embedding_dim) for text in texts]

    def assert_dimension(self) -> None:
        """Smoke-embed once and assert output length matches registry ``embedding_dim``."""
        sample = self.embed(["__forge_dim_check__"])
        if len(sample) != 1 or len(sample[0]) != self._embedding_dim:
            got = len(sample[0]) if sample else 0
            raise ValueError(
                f"adapter dim assert failed for {self._model_id}: "
                f"got {got}, expected {self._embedding_dim}"
            )
        # Cosine of identical input with itself must be 1.0 (L2 unit).
        norm = math.sqrt(sum(v * v for v in sample[0]))
        if abs(norm - 1.0) > 1e-6:
            raise ValueError(
                f"adapter L2 norm assert failed for {self._model_id}: got {norm}, expected ~1.0"
            )
