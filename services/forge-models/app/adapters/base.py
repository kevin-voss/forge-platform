"""ModelAdapter interface shared by all inference backends."""

from __future__ import annotations

from abc import ABC, abstractmethod
from enum import StrEnum
from typing import FrozenSet


class Capability(StrEnum):
    EMBED = "embed"
    GENERATE = "generate"
    CLASSIFY = "classify"
    SUMMARIZE = "summarize"


class HealthStatus(StrEnum):
    OK = "ok"
    DEGRADED = "degraded"
    DOWN = "down"


class ModelAdapter(ABC):
    """Interface every model backend implements."""

    @property
    @abstractmethod
    def model_id(self) -> str:
        """Stable registry identifier."""

    @property
    @abstractmethod
    def backend(self) -> str:
        """Backend family name (fake, local, …)."""

    @property
    @abstractmethod
    def capabilities(self) -> FrozenSet[Capability]:
        """Declared inference capabilities."""

    @property
    @abstractmethod
    def embedding_dim(self) -> int | None:
        """Fixed embedding dimension, or None when embed is unsupported."""

    @abstractmethod
    def health(self) -> HealthStatus:
        """Current adapter health (ok | degraded | down)."""
