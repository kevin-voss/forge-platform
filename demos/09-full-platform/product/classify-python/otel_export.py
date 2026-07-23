"""Minimal OTLP/HTTP JSON span exporter (stdlib only)."""

from __future__ import annotations

import json
import os
import secrets
import time
import urllib.error
import urllib.request
from typing import Optional


def _enabled() -> bool:
    raw = (os.environ.get("FORGE_OTEL_ENABLED") or "").strip().lower()
    return raw in {"1", "true", "yes", "on"}


def _endpoint() -> str:
    ep = (os.environ.get("FORGE_OTEL_EXPORTER_ENDPOINT") or "").strip()
    if not ep:
        ep = (os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT") or "").strip()
    if not ep:
        ep = "http://host.docker.internal:4318"
    # Prefer HTTP/JSON receiver when given gRPC :4317.
    if ep.endswith(":4317"):
        ep = ep[:-5] + ":4318"
    ep = ep.rstrip("/")
    if not ep.endswith("/v1/traces"):
        ep = ep + "/v1/traces"
    return ep


def parse_traceparent(header: Optional[str]) -> tuple[str, str]:
    """Return (trace_id, parent_span_id) hex; mint if absent."""
    if header:
        parts = header.strip().split("-")
        if len(parts) >= 3 and len(parts[1]) == 32 and len(parts[2]) == 16:
            return parts[1], parts[2]
    return secrets.token_hex(16), secrets.token_hex(8)


def export_span(
    *,
    service_name: str,
    span_name: str,
    traceparent: Optional[str] = None,
    status_code: int = 200,
    path: str = "/",
) -> Optional[str]:
    """Export one server span. Returns trace_id or None when disabled/failed."""
    if not _enabled():
        return None
    trace_id, parent_id = parse_traceparent(traceparent)
    span_id = secrets.token_hex(8)
    now_ns = int(time.time() * 1_000_000_000)
    start_ns = now_ns - 5_000_000
    payload = {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": service_name}},
                        {"key": "forge.service", "value": {"stringValue": service_name}},
                    ]
                },
                "scopeSpans": [
                    {
                        "scope": {"name": "incident-classify"},
                        "spans": [
                            {
                                "traceId": trace_id,
                                "spanId": span_id,
                                "parentSpanId": parent_id,
                                "name": span_name,
                                "kind": 2,
                                "startTimeUnixNano": str(start_ns),
                                "endTimeUnixNano": str(now_ns),
                                "attributes": [
                                    {
                                        "key": "http.response.status_code",
                                        "value": {"intValue": status_code},
                                    },
                                    {"key": "url.path", "value": {"stringValue": path}},
                                    {
                                        "key": "forge.service",
                                        "value": {"stringValue": service_name},
                                    },
                                ],
                            }
                        ],
                    }
                ],
            }
        ]
    }
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        _endpoint(),
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=2) as resp:
            resp.read()
    except (urllib.error.URLError, TimeoutError, OSError):
        return trace_id
    return trace_id
