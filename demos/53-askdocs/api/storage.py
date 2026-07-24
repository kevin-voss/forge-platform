"""Forge Storage HTTP client for AskDocs (epic 53.02)."""

from __future__ import annotations

import json
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
    project = (env.get("FORGE_STORAGE_PROJECT") or env.get("FORGE_PROJECT") or "").strip() or "askdocs"
    bucket = (env.get("FORGE_STORAGE_BUCKET") or "").strip() or "askdocs-corpus"
    return StorageConfig(
        base_url=base.rstrip("/"),
        project_id=project,
        bucket=bucket,
    )


def encode_object_key(key: str) -> str:
    parts = key.split("/")
    return "/".join(urllib.parse.quote(p, safe="") for p in parts)


class StorageClient:
    def __init__(self, cfg: StorageConfig | None = None) -> None:
        self.cfg = cfg or load_storage_config()

    def _headers(self, content_type: str | None = None) -> dict[str, str]:
        headers = {"X-Forge-Project": self.cfg.project_id}
        if content_type:
            headers["Content-Type"] = content_type
        return headers

    def _object_url(self, key: str) -> str:
        return (
            f"{self.cfg.base_url}/v1/buckets/"
            f"{urllib.parse.quote(self.cfg.bucket, safe='')}/objects/{encode_object_key(key)}"
        )

    def _request(
        self,
        method: str,
        url: str,
        data: bytes | None = None,
        headers: dict[str, str] | None = None,
        timeout: float = 60.0,
    ) -> tuple[int, bytes, dict[str, str]]:
        req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                body = resp.read()
                hdrs = {k.lower(): v for k, v in resp.headers.items()}
                return int(resp.status), body, hdrs
        except urllib.error.HTTPError as exc:
            body = exc.read() if exc.fp else b""
            hdrs = {k.lower(): v for k, v in (exc.headers.items() if exc.headers else [])}
            return int(exc.code), body, hdrs

    def ping(self) -> None:
        code, body, _ = self._request("GET", f"{self.cfg.base_url}/health/ready", timeout=10.0)
        if code != 200:
            raise RuntimeError(f"storage ready HTTP {code}: {body[:256]!r}")

    def ensure_bucket(self) -> None:
        payload = json.dumps({"name": self.cfg.bucket}).encode()
        code, body, _ = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/buckets",
            data=payload,
            headers=self._headers("application/json"),
            timeout=30.0,
        )
        if code not in (200, 201, 409):
            raise RuntimeError(f"create bucket HTTP {code}: {body[:512]!r}")

    def put_object(self, key: str, data: bytes, content_type: str = "text/plain") -> None:
        if content_type == "":
            content_type = "application/octet-stream"
        code, body, _ = self._request(
            "PUT",
            self._object_url(key),
            data=data,
            headers=self._headers(content_type),
            timeout=60.0,
        )
        if code not in (200, 201):
            raise RuntimeError(f"put object HTTP {code}: {body[:512]!r}")

    def get_object(self, key: str) -> tuple[bytes, str]:
        code, body, hdrs = self._request(
            "GET",
            self._object_url(key),
            headers=self._headers(),
            timeout=60.0,
        )
        if code != 200:
            raise RuntimeError(f"get object HTTP {code}: {body[:512]!r}")
        ct = hdrs.get("content-type") or "application/octet-stream"
        return body, ct
