"""Load and serve the in-memory model registry from models.yaml."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Mapping

import yaml

from app.adapters.base import Capability, HealthStatus, ModelAdapter
from app.adapters.fake import FakeAdapter
from app.adapters.local_embed import LocalEmbeddingAdapter
from app.adapters.local_gen import LocalGenerationAdapter

logger = logging.getLogger("forge-models")

DEFAULT_MODELS_CONFIG = Path(__file__).resolve().parent / "models.yaml"

_HEALTH_GAUGE = {
    HealthStatus.OK: 1.0,
    HealthStatus.DEGRADED: 0.5,
    HealthStatus.DOWN: 0.0,
}


class RegistryLoadError(ValueError):
    """Raised when models.yaml is missing or malformed."""


@dataclass
class RegistryMetrics:
    """In-process gauges until epic 14.06 wires Prometheus/OTEL."""

    models_registry_size: int = 0
    model_health: dict[str, float] = field(default_factory=dict)
    models_embed_requests_total: int = 0
    models_embed_latency_seconds: float = 0.0
    models_generate_requests_total: int = 0
    models_generate_latency_seconds: float = 0.0
    models_classify_requests_total: int = 0
    models_classify_latency_seconds: float = 0.0
    models_summarize_requests_total: int = 0
    models_summarize_latency_seconds: float = 0.0

    def refresh(self, adapters: Mapping[str, ModelAdapter]) -> None:
        self.models_registry_size = len(adapters)
        self.model_health = {
            model_id: _HEALTH_GAUGE[adapter.health()] for model_id, adapter in adapters.items()
        }

    def record_embed(self, latency_seconds: float) -> None:
        self.models_embed_requests_total += 1
        self.models_embed_latency_seconds += max(0.0, latency_seconds)

    def record_generate(self, latency_seconds: float) -> None:
        self.models_generate_requests_total += 1
        self.models_generate_latency_seconds += max(0.0, latency_seconds)

    def record_classify(self, latency_seconds: float) -> None:
        self.models_classify_requests_total += 1
        self.models_classify_latency_seconds += max(0.0, latency_seconds)

    def record_summarize(self, latency_seconds: float) -> None:
        self.models_summarize_requests_total += 1
        self.models_summarize_latency_seconds += max(0.0, latency_seconds)


@dataclass
class ModelRegistry:
    """In-memory map of model_id → adapter."""

    adapters: dict[str, ModelAdapter]
    metrics: RegistryMetrics = field(default_factory=RegistryMetrics)

    def __post_init__(self) -> None:
        self.metrics.refresh(self.adapters)

    def get(self, model_id: str) -> ModelAdapter | None:
        return self.adapters.get(model_id)

    def list(self) -> list[ModelAdapter]:
        return [self.adapters[key] for key in sorted(self.adapters)]

    def refresh_metrics(self) -> None:
        self.metrics.refresh(self.adapters)


def resolve_config_path(config_path: str | None) -> Path:
    """Resolve FORGE_MODELS_CONFIG or the packaged default models.yaml."""
    if config_path and config_path.strip():
        return Path(config_path).expanduser().resolve()
    return DEFAULT_MODELS_CONFIG


def load_registry(
    config_path: str | None = None,
    *,
    local_model_path: str | None = None,
) -> ModelRegistry:
    """Parse models.yaml and instantiate adapters. Fails fast on bad config."""
    path = resolve_config_path(config_path)
    if not path.is_file():
        raise RegistryLoadError(f"models config not found: {path}")

    try:
        raw = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as exc:
        raise RegistryLoadError(f"malformed models.yaml ({path}): {exc}") from exc

    if raw is None:
        raise RegistryLoadError(f"malformed models.yaml ({path}): document is empty")
    if not isinstance(raw, dict):
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): root must be a mapping, got {type(raw).__name__}"
        )

    models = raw.get("models")
    if models is None:
        raise RegistryLoadError(f"malformed models.yaml ({path}): missing required key 'models'")
    if not isinstance(models, list):
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): 'models' must be a list, got {type(models).__name__}"
        )
    if len(models) == 0:
        raise RegistryLoadError(f"malformed models.yaml ({path}): 'models' list is empty")

    adapters: dict[str, ModelAdapter] = {}
    for index, entry in enumerate(models):
        adapter = _adapter_from_entry(
            entry,
            path=path,
            index=index,
            local_model_path=local_model_path,
        )
        if adapter.model_id in adapters:
            raise RegistryLoadError(
                f"malformed models.yaml ({path}): duplicate model id '{adapter.model_id}'"
            )
        adapters[adapter.model_id] = adapter

    for adapter in adapters.values():
        if isinstance(adapter, LocalEmbeddingAdapter):
            try:
                adapter.assert_dimension()
            except ValueError as exc:
                raise RegistryLoadError(str(exc)) from exc

    registry = ModelRegistry(adapters=adapters)
    logger.info(
        "registry loaded",
        extra={
            "count": len(adapters),
            "ids": sorted(adapters),
            "backends": sorted({a.backend for a in adapters.values()}),
        },
    )
    return registry


def _adapter_from_entry(
    entry: Any,
    *,
    path: Path,
    index: int,
    local_model_path: str | None = None,
) -> ModelAdapter:
    loc = f"models[{index}]"
    if not isinstance(entry, dict):
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): {loc} must be a mapping, got {type(entry).__name__}"
        )

    model_id = entry.get("id")
    if not isinstance(model_id, str) or not model_id.strip():
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): {loc}.id must be a non-empty string"
        )
    model_id = model_id.strip()

    backend = entry.get("backend")
    if not isinstance(backend, str) or not backend.strip():
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): {loc} ({model_id}).backend must be a non-empty string"
        )
    backend = backend.strip().lower()

    caps_raw = entry.get("capabilities")
    if not isinstance(caps_raw, list) or len(caps_raw) == 0:
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): {loc} ({model_id}).capabilities "
            "must be a non-empty list"
        )

    capabilities: set[Capability] = set()
    for cap in caps_raw:
        if not isinstance(cap, str):
            raise RegistryLoadError(
                f"malformed models.yaml ({path}): {loc} ({model_id}).capabilities "
                f"entries must be strings, got {type(cap).__name__}"
            )
        try:
            capabilities.add(Capability(cap.strip().lower()))
        except ValueError as exc:
            raise RegistryLoadError(
                f"malformed models.yaml ({path}): {loc} ({model_id}).capabilities "
                f"unknown value '{cap}' (allowed: embed, generate, classify, summarize)"
            ) from exc

    embedding_dim = entry.get("embedding_dim")
    if embedding_dim is not None:
        if (
            not isinstance(embedding_dim, int)
            or isinstance(embedding_dim, bool)
            or embedding_dim < 1
        ):
            raise RegistryLoadError(
                f"malformed models.yaml ({path}): {loc} ({model_id}).embedding_dim "
                "must be a positive integer when set"
            )

    if Capability.EMBED in capabilities and embedding_dim is None:
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): {loc} ({model_id}) has embed capability "
            "but missing embedding_dim"
        )

    if backend not in {"fake", "local"}:
        raise RegistryLoadError(
            f"malformed models.yaml ({path}): {loc} ({model_id}).backend "
            f"unknown value '{backend}' (allowed: fake, local)"
        )

    gen_caps = {Capability.GENERATE, Capability.CLASSIFY, Capability.SUMMARIZE}

    # Embed models use LocalEmbeddingAdapter (deterministic CI + optional on-disk model).
    if Capability.EMBED in capabilities:
        assert embedding_dim is not None  # validated above
        # Optional transformer only for backend=local when FORGE_MODELS_LOCAL_MODEL_PATH is set.
        path_for_adapter = local_model_path if backend == "local" else None
        try:
            return LocalEmbeddingAdapter(
                model_id=model_id,
                backend=backend,
                capabilities=capabilities,
                embedding_dim=embedding_dim,
                local_model_path=path_for_adapter,
            )
        except ValueError as exc:
            raise RegistryLoadError(str(exc)) from exc

    # Generate/classify/summarize models use LocalGenerationAdapter (deterministic fake).
    if capabilities & gen_caps:
        try:
            return LocalGenerationAdapter(
                model_id=model_id,
                backend=backend,
                capabilities=capabilities,
            )
        except ValueError as exc:
            raise RegistryLoadError(str(exc)) from exc

    return FakeAdapter(
        model_id=model_id,
        backend=backend,
        capabilities=capabilities,
        embedding_dim=embedding_dim,
    )


def serialize_model(adapter: ModelAdapter) -> dict[str, Any]:
    """Serialize a registry entry for HTTP responses."""
    return {
        "id": adapter.model_id,
        "capabilities": sorted(c.value for c in adapter.capabilities),
        "backend": adapter.backend,
        "embedding_dim": adapter.embedding_dim,
        "status": adapter.health().value,
    }
