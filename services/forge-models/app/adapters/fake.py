"""Deterministic placeholder adapter for registry population (no inference)."""

from __future__ import annotations

from typing import FrozenSet, Iterable

from app.adapters.base import Capability, HealthStatus, ModelAdapter


class FakeAdapter(ModelAdapter):
    """Placeholder adapter: metadata + always-ok health; no real inference yet."""

    def __init__(
        self,
        *,
        model_id: str,
        backend: str,
        capabilities: Iterable[Capability],
        embedding_dim: int | None = None,
        health_status: HealthStatus = HealthStatus.OK,
    ) -> None:
        self._model_id = model_id
        self._backend = backend
        self._capabilities = frozenset(capabilities)
        self._embedding_dim = embedding_dim
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
        return self._embedding_dim

    def health(self) -> HealthStatus:
        return self._health
