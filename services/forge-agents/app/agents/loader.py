"""Load agent YAML definitions into a validated in-memory registry."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml
from pydantic import ValidationError

from app.agents.models import AgentDefinition

logger = logging.getLogger("forge-agents")

# Default: services/forge-agents/agents (repo-relative path named in the step).
DEFAULT_AGENTS_DIR = Path(__file__).resolve().parents[2] / "agents"


class AgentLoadError(ValueError):
    """Raised when agent YAML is missing, malformed, or fails validation."""


@dataclass
class AgentRegistry:
    """In-memory map of agent name → definition."""

    agents: dict[str, AgentDefinition]
    agents_registry_size: int = field(init=False)

    def __post_init__(self) -> None:
        self.agents_registry_size = len(self.agents)

    def get(self, name: str) -> AgentDefinition | None:
        return self.agents.get(name)

    def list(self) -> list[AgentDefinition]:
        return [self.agents[key] for key in sorted(self.agents)]


def resolve_defs_dir(defs_dir: str | None) -> Path:
    """Resolve FORGE_AGENTS_DEFS_DIR or the packaged default agents/ directory."""
    if defs_dir and defs_dir.strip():
        return Path(defs_dir).expanduser().resolve()
    return DEFAULT_AGENTS_DIR.resolve()


def load_registry(defs_dir: str | None = None) -> AgentRegistry:
    """Glob YAML under defs_dir, validate, and build a registry. Fail fast."""
    directory = resolve_defs_dir(defs_dir)
    if not directory.is_dir():
        raise AgentLoadError(f"agent definitions directory not found: {directory}")

    paths = sorted(directory.glob("*.yaml")) + sorted(directory.glob("*.yml"))
    # Deduplicate when both globs could match the same path (unlikely).
    seen_paths: set[Path] = set()
    unique_paths: list[Path] = []
    for path in paths:
        resolved = path.resolve()
        if resolved in seen_paths:
            continue
        seen_paths.add(resolved)
        unique_paths.append(path)

    if not unique_paths:
        raise AgentLoadError(
            f"no agent YAML files found in {directory} (expected at least one *.yaml / *.yml)"
        )

    agents: dict[str, AgentDefinition] = {}
    for path in unique_paths:
        definition = _load_file(path)
        if definition.name in agents:
            raise AgentLoadError(
                f"duplicate agent name '{definition.name}' "
                f"(already loaded; conflicting file: {path})"
            )
        agents[definition.name] = definition

    registry = AgentRegistry(agents=agents)
    names = sorted(agents)
    logger.info(
        "agents registry loaded",
        extra={
            "agents_registry_size": registry.agents_registry_size,
            "agent_names": names,
            "agents_defs_dir": str(directory),
        },
    )
    return registry


def _load_file(path: Path) -> AgentDefinition:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise AgentLoadError(f"cannot read agent YAML ({path}): {exc}") from exc

    try:
        raw: Any = yaml.safe_load(text)
    except yaml.YAMLError as exc:
        raise AgentLoadError(f"malformed agent YAML ({path}): {exc}") from exc

    if raw is None:
        raise AgentLoadError(f"malformed agent YAML ({path}): document is empty")
    if not isinstance(raw, dict):
        raise AgentLoadError(
            f"malformed agent YAML ({path}): root must be a mapping, got {type(raw).__name__}"
        )

    try:
        return AgentDefinition.model_validate(raw)
    except ValidationError as exc:
        raise AgentLoadError(
            f"invalid agent definition ({path}): {_format_validation(exc)}"
        ) from exc


def _format_validation(exc: ValidationError) -> str:
    parts: list[str] = []
    for err in exc.errors():
        loc = ".".join(str(item) for item in err.get("loc", ()))
        msg = err.get("msg", "invalid")
        parts.append(f"{loc}: {msg}" if loc else msg)
    return "; ".join(parts) if parts else str(exc)
