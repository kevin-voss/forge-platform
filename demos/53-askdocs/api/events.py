"""Forge Events publish client for AskDocs (epic 53.02)."""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any


@dataclass
class EventsConfig:
    base_url: str
    source: str
    subject: str


def load_events_config(environ: dict[str, str] | None = None) -> EventsConfig:
    env = environ if environ is not None else os.environ
    base = (env.get("FORGE_EVENTS_URL") or "").strip() or "http://host.docker.internal:4105"
    source = (env.get("FORGE_SERVICE_NAME") or "").strip() or "askdocs-api"
    subject = (env.get("FORGE_EVENTS_SUBJECT") or "").strip() or "document.uploaded"
    return EventsConfig(base_url=base.rstrip("/"), source=source, subject=subject)


class EventsClient:
    def __init__(self, cfg: EventsConfig | None = None) -> None:
        self.cfg = cfg or load_events_config()

    def _request(
        self,
        method: str,
        url: str,
        data: bytes | None = None,
        headers: dict[str, str] | None = None,
        timeout: float = 15.0,
    ) -> tuple[int, bytes]:
        req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                return int(resp.status), resp.read()
        except urllib.error.HTTPError as exc:
            body = exc.read() if exc.fp else b""
            return int(exc.code), body

    def ping(self) -> None:
        code, body = self._request("GET", f"{self.cfg.base_url}/health/ready", timeout=10.0)
        if code != 200:
            raise RuntimeError(f"events ready HTTP {code}: {body[:256]!r}")

    def publish_document_uploaded(
        self,
        document_id: str,
        object_key: str,
        content_type: str,
        title: str = "",
        uploaded_at: str | None = None,
    ) -> None:
        uploaded_at = uploaded_at or datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        data: dict[str, Any] = {
            "document_id": document_id,
            "object_key": object_key,
            "content_type": content_type,
            "uploaded_at": uploaded_at,
            "source": self.cfg.source,
        }
        if title:
            data["title"] = title
        payload = {
            "subject": self.cfg.subject,
            "source": self.cfg.source,
            "data": data,
        }
        raw = json.dumps(payload).encode()
        headers = {
            "Content-Type": "application/json",
            "Idempotency-Key": document_id,
        }
        code, body = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/events",
            data=raw,
            headers=headers,
            timeout=15.0,
        )
        if code != 202:
            raise RuntimeError(f"events publish HTTP {code}: {body[:1024]!r}")
