"""Forge Memory HTTP client for AskDocs chunk vectors (epic 53.03)."""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any, Sequence

from embeddings import (
    EMBEDDING_DIM,
    MEMORY_COLLECTION,
    MEMORY_DISTANCE,
    assert_embedding_contract,
)


@dataclass
class MemoryConfig:
    base_url: str
    project_id: str
    collection: str
    dim: int
    distance: str


@dataclass
class MemoryHit:
    id: str
    score: float
    metadata: dict[str, Any]


def load_memory_config(environ: dict[str, str] | None = None) -> MemoryConfig:
    env = environ if environ is not None else os.environ
    base = (env.get("FORGE_MEMORY_URL") or "").strip() or "http://host.docker.internal:4303"
    project = (
        env.get("FORGE_MEMORY_PROJECT")
        or env.get("FORGE_STORAGE_PROJECT")
        or env.get("FORGE_PROJECT")
        or ""
    ).strip() or "askdocs"
    collection = (env.get("FORGE_MEMORY_COLLECTION") or "").strip() or MEMORY_COLLECTION
    dim_raw = (env.get("FORGE_MEMORY_DIM") or env.get("FORGE_MODELS_EMBED_DIM") or "").strip()
    dim = int(dim_raw) if dim_raw else EMBEDDING_DIM
    distance = (env.get("FORGE_MEMORY_DISTANCE") or "").strip() or MEMORY_DISTANCE
    return MemoryConfig(
        base_url=base.rstrip("/"),
        project_id=project,
        collection=collection,
        dim=dim,
        distance=distance,
    )


class MemoryClient:
    def __init__(self, cfg: MemoryConfig | None = None) -> None:
        self.cfg = cfg or load_memory_config()

    def _headers(self, content_type: str | None = None) -> dict[str, str]:
        headers = {"X-Forge-Project": self.cfg.project_id}
        if content_type:
            headers["Content-Type"] = content_type
        return headers

    def _collection_url(self, suffix: str = "") -> str:
        name = urllib.parse.quote(self.cfg.collection, safe="")
        return f"{self.cfg.base_url}/v1/collections/{name}{suffix}"

    def _request(
        self,
        method: str,
        url: str,
        data: bytes | None = None,
        headers: dict[str, str] | None = None,
        timeout: float = 60.0,
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
            raise RuntimeError(f"memory ready HTTP {code}: {body[:256]!r}")

    def ensure_collection(self) -> None:
        get_code, get_body = self._request(
            "GET",
            self._collection_url(),
            headers=self._headers(),
            timeout=30.0,
        )
        if get_code == 200:
            try:
                data = json.loads(get_body.decode() or "{}")
            except json.JSONDecodeError as exc:
                raise RuntimeError(f"memory get collection invalid JSON: {get_body[:256]!r}") from exc
            dim = int(data.get("dim") or 0)
            if dim != self.cfg.dim:
                raise RuntimeError(
                    f"memory collection dim mismatch: got {dim}, expected {self.cfg.dim} "
                    f"(collection={self.cfg.collection}) — Models↔Memory contract finding"
                )
            return
        payload = json.dumps(
            {
                "name": self.cfg.collection,
                "dim": self.cfg.dim,
                "distance": self.cfg.distance,
            }
        ).encode()
        code, body = self._request(
            "POST",
            f"{self.cfg.base_url}/v1/collections",
            data=payload,
            headers=self._headers("application/json"),
            timeout=30.0,
        )
        if code in (200, 201, 409):
            return
        raise RuntimeError(f"create collection HTTP {code}: {body[:512]!r}")

    def upsert(
        self,
        records: Sequence[dict[str, Any]],
    ) -> int:
        """Upsert raw vectors. Each record: {id, vector, metadata?}."""
        if not records:
            return 0
        normalized: list[dict[str, Any]] = []
        for rec in records:
            rid = str(rec.get("id") or "").strip()
            if not rid:
                raise ValueError("upsert record missing id")
            vector = assert_embedding_contract(rec["vector"], dim=self.cfg.dim)
            item: dict[str, Any] = {
                "id": rid,
                "vector": vector,
                "metadata": dict(rec.get("metadata") or {}),
            }
            if rec.get("document_ref"):
                item["document_ref"] = str(rec["document_ref"])
            normalized.append(item)
        payload = json.dumps({"records": normalized}).encode()
        code, body = self._request(
            "POST",
            self._collection_url("/upsert"),
            data=payload,
            headers=self._headers("application/json"),
            timeout=60.0,
        )
        if code != 200:
            raise RuntimeError(f"memory upsert HTTP {code}: {body[:512]!r}")
        try:
            data = json.loads(body.decode() or "{}")
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"memory upsert invalid JSON: {body[:256]!r}") from exc
        return int(data.get("upserted") or 0)

    def query(self, vector: Sequence[float], top_k: int = 5) -> list[MemoryHit]:
        vec = assert_embedding_contract(vector, dim=self.cfg.dim)
        top_k = max(1, int(top_k))
        payload = json.dumps({"vector": vec, "top_k": top_k}).encode()
        code, body = self._request(
            "POST",
            self._collection_url("/query"),
            data=payload,
            headers=self._headers("application/json"),
            timeout=60.0,
        )
        if code != 200:
            raise RuntimeError(f"memory query HTTP {code}: {body[:512]!r}")
        try:
            data = json.loads(body.decode() or "{}")
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"memory query invalid JSON: {body[:256]!r}") from exc
        hits: list[MemoryHit] = []
        for item in data.get("results") or []:
            if not isinstance(item, dict):
                continue
            rid = str(item.get("id") or "").strip()
            if not rid:
                continue
            meta = item.get("metadata") or {}
            if not isinstance(meta, dict):
                meta = {}
            hits.append(
                MemoryHit(
                    id=rid,
                    score=float(item.get("score") or 0.0),
                    metadata=dict(meta),
                )
            )
        return hits
