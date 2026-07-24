"""Forge Events consumer client for AskDocs ingest worker (epic 53.02)."""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


@dataclass
class EventsConfig:
    base_url: str
    consumer: str
    identity: str
    subject: str
    ack_wait_s: int
    max_deliveries: int
    poll_ms: int
    batch: int


def _env_int(env: dict[str, str], key: str, default: int) -> int:
    raw = (env.get(key) or "").strip()
    if not raw:
        return default
    try:
        n = int(raw)
    except ValueError:
        return default
    return n if n > 0 else default


def load_events_config(environ: dict[str, str] | None = None) -> EventsConfig:
    env = environ if environ is not None else os.environ
    base = (env.get("FORGE_EVENTS_URL") or "").strip() or "http://host.docker.internal:4105"
    consumer = (env.get("FORGE_EVENTS_CONSUMER") or "").strip() or "askdocs-documents"
    identity = (env.get("FORGE_EVENTS_CONSUMER_IDENTITY") or "").strip() or consumer
    subject = (env.get("FORGE_EVENTS_SUBJECT") or "").strip() or "document.uploaded"
    return EventsConfig(
        base_url=base.rstrip("/"),
        consumer=consumer,
        identity=identity,
        subject=subject,
        ack_wait_s=_env_int(env, "FORGE_DEFAULT_ACK_WAIT_S", 30),
        max_deliveries=_env_int(env, "FORGE_DEFAULT_MAX_DELIVERIES", 5),
        poll_ms=_env_int(env, "FORGE_EVENTS_POLL_MS", 500),
        batch=_env_int(env, "FORGE_EVENTS_BATCH", 8),
    )


@dataclass
class DeliveredMessage:
    event_id: str
    subject: str
    ack_token: str
    delivery_count: int
    data: dict[str, Any]


class EventsClient:
    def __init__(self, cfg: EventsConfig | None = None) -> None:
        self.cfg = cfg or load_events_config()

    def _request(
        self,
        method: str,
        url: str,
        data: bytes | None = None,
        headers: dict[str, str] | None = None,
        timeout: float = 45.0,
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

    def ensure_consumer(self) -> None:
        payload = {
            "name": self.cfg.consumer,
            "subject": self.cfg.subject,
            "ack_wait_s": self.cfg.ack_wait_s,
            "max_deliveries": self.cfg.max_deliveries,
            "identity": self.cfg.identity,
        }
        raw = json.dumps(payload).encode()
        code, body = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/consumers",
            data=raw,
            headers={"Content-Type": "application/json"},
            timeout=15.0,
        )
        if code not in (200, 201):
            raise RuntimeError(f"create consumer HTTP {code}: {body[:512]!r}")

    def consume(self) -> list[DeliveredMessage]:
        payload = {"consumer": self.cfg.consumer, "batch": self.cfg.batch}
        raw = json.dumps(payload).encode()
        code, body = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/consume",
            data=raw,
            headers={"Content-Type": "application/json"},
            timeout=45.0,
        )
        if code != 200:
            raise RuntimeError(f"consume HTTP {code}: {body[:512]!r}")
        parsed = json.loads(body.decode() or "{}")
        out: list[DeliveredMessage] = []
        for item in parsed.get("messages") or []:
            data = item.get("data") or {}
            if isinstance(data, str):
                try:
                    data = json.loads(data)
                except json.JSONDecodeError:
                    data = {}
            if not isinstance(data, dict):
                data = {}
            out.append(
                DeliveredMessage(
                    event_id=str(item.get("event_id") or ""),
                    subject=str(item.get("subject") or ""),
                    ack_token=str(item.get("ack_token") or ""),
                    delivery_count=int(item.get("delivery_count") or 0),
                    data=data,
                )
            )
        return out

    def mark_processed(self, event_id: str) -> None:
        raw = json.dumps({"consumer": self.cfg.consumer, "event_id": event_id}).encode()
        code, body = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/processed",
            data=raw,
            headers={"Content-Type": "application/json"},
            timeout=15.0,
        )
        if code not in (200, 204):
            raise RuntimeError(f"processed HTTP {code}: {body[:256]!r}")

    def ack(self, ack_token: str) -> None:
        raw = json.dumps({"ack_token": ack_token}).encode()
        code, body = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/ack",
            data=raw,
            headers={"Content-Type": "application/json"},
            timeout=15.0,
        )
        if code not in (200, 204):
            raise RuntimeError(f"ack HTTP {code}: {body[:256]!r}")

    def nak(self, ack_token: str, delay_s: int = 0) -> None:
        payload: dict[str, Any] = {"ack_token": ack_token}
        if delay_s > 0:
            payload["delay_s"] = delay_s
        raw = json.dumps(payload).encode()
        code, body = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/nak",
            data=raw,
            headers={"Content-Type": "application/json"},
            timeout=15.0,
        )
        if code not in (200, 204):
            raise RuntimeError(f"nak HTTP {code}: {body[:256]!r}")
