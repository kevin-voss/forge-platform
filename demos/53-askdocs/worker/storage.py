"""Forge Storage client for AskDocs ingest worker (epic 53.02)."""

from __future__ import annotations

import os
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass


@dataclass
class StorageConfig:
    base_url: str
    project_id: str
    bucket: str


def load_storage_config(environ: dict[str, str] | None = None) -> StorageConfig:
    env = environ if environ is not None else os.environ
    base = (env.get("FORGE_STORAGE_URL") or "").strip() or "http://host.docker.internal:4107"
    project = (env.get("FORGE_STORAGE_PROJECT") or "").strip() or "askdocs"
    bucket = (env.get("FORGE_STORAGE_BUCKET") or "").strip() or "askdocs-corpus"
    return StorageConfig(base_url=base.rstrip("/"), project_id=project, bucket=bucket)


def encode_object_key(key: str) -> str:
    return "/".join(urllib.parse.quote(p, safe="") for p in key.split("/"))


class StorageClient:
    def __init__(self, cfg: StorageConfig | None = None) -> None:
        self.cfg = cfg or load_storage_config()

    def _object_url(self, key: str) -> str:
        return (
            f"{self.cfg.base_url}/v1/buckets/"
            f"{urllib.parse.quote(self.cfg.bucket, safe='')}/objects/{encode_object_key(key)}"
        )

    def _request(self, method: str, url: str, headers: dict[str, str] | None = None, timeout: float = 60.0):
        req = urllib.request.Request(url, method=method, headers=headers or {})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                return int(resp.status), resp.read(), {k.lower(): v for k, v in resp.headers.items()}
        except urllib.error.HTTPError as exc:
            body = exc.read() if exc.fp else b""
            return int(exc.code), body, {}

    def ping(self) -> None:
        code, body, _ = self._request("GET", f"{self.cfg.base_url}/health/ready", timeout=10.0)
        if code != 200:
            raise RuntimeError(f"storage ready HTTP {code}: {body[:256]!r}")

    def get_object(self, key: str) -> bytes:
        code, body, _ = self._request(
            "GET",
            self._object_url(key),
            headers={"X-Forge-Project": self.cfg.project_id},
            timeout=60.0,
        )
        if code != 200:
            raise RuntimeError(f"get object HTTP {code}: {body[:512]!r}")
        return body
