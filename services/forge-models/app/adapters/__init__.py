"""Model backend adapters."""

from app.adapters.base import Capability, HealthStatus, ModelAdapter
from app.adapters.fake import FakeAdapter
from app.adapters.local_embed import LocalEmbeddingAdapter, deterministic_embed, l2_normalize

__all__ = [
    "Capability",
    "FakeAdapter",
    "HealthStatus",
    "LocalEmbeddingAdapter",
    "ModelAdapter",
    "deterministic_embed",
    "l2_normalize",
]
