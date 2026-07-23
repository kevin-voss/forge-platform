"""Model backend adapters."""

from app.adapters.base import Capability, HealthStatus, ModelAdapter
from app.adapters.fake import FakeAdapter
from app.adapters.local_embed import LocalEmbeddingAdapter, deterministic_embed, l2_normalize
from app.adapters.local_gen import LocalGenerationAdapter, summarize_prompt

__all__ = [
    "Capability",
    "FakeAdapter",
    "HealthStatus",
    "LocalEmbeddingAdapter",
    "LocalGenerationAdapter",
    "ModelAdapter",
    "deterministic_embed",
    "l2_normalize",
    "summarize_prompt",
]
