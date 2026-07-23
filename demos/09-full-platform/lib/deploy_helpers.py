"""Deploy orchestration helpers for demos/09-full-platform (step 19.02).

Pure parsing / polling helpers so deploy.sh stays thin and unit-testable.
"""

from __future__ import annotations

import json
import time
from typing import Any, Callable, Mapping, Optional


def parse_json_id(payload: str | Mapping[str, Any], key: str = "id") -> str:
    """Extract a non-empty string id from a JSON object or JSON text."""
    data = json.loads(payload) if isinstance(payload, str) else payload
    value = data.get(key)
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"missing {key!r} in {data!r}")
    return value.strip()


def parse_build_accept(payload: str | Mapping[str, Any]) -> tuple[str, str]:
    """Return (build_id, status) from POST /v1/builds accept body."""
    data = json.loads(payload) if isinstance(payload, str) else payload
    build_id = data.get("buildId")
    status = data.get("status")
    if not isinstance(build_id, str) or not build_id.strip():
        raise ValueError(f"missing buildId in {data!r}")
    if not isinstance(status, str) or not status.strip():
        raise ValueError(f"missing status in {data!r}")
    return build_id.strip(), status.strip()


def parse_build_image(payload: str | Mapping[str, Any]) -> Optional[str]:
    """Return image ref from a build record, or None if not yet recorded."""
    data = json.loads(payload) if isinstance(payload, str) else payload
    image = data.get("image")
    if image is None or image == "":
        return None
    if not isinstance(image, str):
        raise ValueError(f"image must be a string, got {image!r}")
    return image


def deployment_status(payload: str | Mapping[str, Any]) -> str:
    """Return deployment status string."""
    data = json.loads(payload) if isinstance(payload, str) else payload
    status = data.get("status")
    if not isinstance(status, str) or not status.strip():
        raise ValueError(f"missing status in {data!r}")
    return status.strip()


def route_hosts(payload: str | Mapping[str, Any] | list[Any]) -> set[str]:
    """Collect lowercase hosts from GET /admin/routes."""
    data = json.loads(payload) if isinstance(payload, str) else payload
    if not isinstance(data, list):
        raise ValueError(f"routes payload must be a list, got {type(data).__name__}")
    hosts: set[str] = set()
    for row in data:
        if not isinstance(row, Mapping):
            continue
        host = row.get("host")
        if isinstance(host, str) and host.strip():
            hosts.add(host.strip().lower())
    return hosts


def wait_until(
    predicate: Callable[[], bool],
    *,
    timeout_s: float = 60.0,
    interval_s: float = 1.0,
    label: str = "condition",
) -> None:
    """Poll predicate until true or raise TimeoutError."""
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(interval_s)
    raise TimeoutError(f"timed out waiting for {label} after {timeout_s:g}s")


def product_hostnames(service_names: list[str], pattern: str = "{service}.demo.localhost") -> dict[str, str]:
    """Map Control service name → Gateway hostname from FORGE_HOST_PATTERN."""
    if "{service}" not in pattern:
        raise ValueError("pattern must contain '{service}'")
    return {name: pattern.replace("{service}", name) for name in service_names}
