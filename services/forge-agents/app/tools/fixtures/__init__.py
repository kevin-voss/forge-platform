"""Deterministic fake responses for platform tools (CI)."""

from __future__ import annotations

import json
from functools import lru_cache
from pathlib import Path
from typing import Any

_FIXTURE_DIR = Path(__file__).resolve().parent


@lru_cache(maxsize=1)
def load_fixtures() -> dict[str, Any]:
    """Load all JSON fixtures keyed by tool name."""
    out: dict[str, Any] = {}
    for path in sorted(_FIXTURE_DIR.glob("*.json")):
        out[path.stem] = json.loads(path.read_text(encoding="utf-8"))
    return out


def fixture_for(tool_name: str) -> dict[str, Any]:
    """Return a shallow copy of the fixture payload for `tool_name`."""
    data = load_fixtures().get(tool_name)
    if data is None:
        raise KeyError(f"missing fixture for tool '{tool_name}'")
    return dict(data)
