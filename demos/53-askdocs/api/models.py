"""Forge Models HTTP client for AskDocs embeddings (epic 53.03)."""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Sequence

from embeddings import (
    EMBEDDING_DIM,
    EMBEDDING_MODEL,
    EmbeddingContractError,
    assert_embedding_contract,
)


@dataclass
class ModelsConfig:
    base_url: str
    model: str
    expected_dim: int


def load_models_config(environ: dict[str, str] | None = None) -> ModelsConfig:
    env = environ if environ is not None else os.environ
    base = (env.get("FORGE_MODELS_URL") or "").strip() or "http://host.docker.internal:4300"
    model = (env.get("FORGE_MODELS_EMBED_MODEL") or env.get("FORGE_MEMORY_DEFAULT_MODEL") or "").strip()
    if not model:
        model = EMBEDDING_MODEL
    dim_raw = (env.get("FORGE_MODELS_EMBED_DIM") or "").strip()
    dim = int(dim_raw) if dim_raw else EMBEDDING_DIM
    return ModelsConfig(base_url=base.rstrip("/"), model=model, expected_dim=dim)


class ModelsClient:
    def __init__(self, cfg: ModelsConfig | None = None) -> None:
        self.cfg = cfg or load_models_config()

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
            raise RuntimeError(f"models ready HTTP {code}: {body[:256]!r}")

    def embed(self, text: str) -> list[float]:
        vectors = self.embed_batch([text])
        return vectors[0]

    def embed_batch(self, texts: Sequence[str]) -> list[list[float]]:
        inputs = [str(t) for t in texts]
        if not inputs:
            return []
        if any(not t.strip() for t in inputs):
            raise EmbeddingContractError("embed input must be non-empty")
        payload = json.dumps({"input": inputs if len(inputs) > 1 else inputs[0]}).encode()
        url = (
            f"{self.cfg.base_url}/v1/models/"
            f"{urllib.parse.quote(self.cfg.model, safe='')}/embed"
        )
        code, body = self._request(
            "POST",
            url,
            data=payload,
            headers={"Content-Type": "application/json"},
            timeout=60.0,
        )
        if code != 200:
            raise RuntimeError(f"models embed HTTP {code}: {body[:512]!r}")
        try:
            data = json.loads(body.decode() or "{}")
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"models embed invalid JSON: {body[:256]!r}") from exc
        dim = int(data.get("dim") or 0)
        if dim and dim != self.cfg.expected_dim:
            raise EmbeddingContractError(
                f"models reported dim={dim}, expected {self.cfg.expected_dim} "
                f"(model={self.cfg.model})"
            )
        embeddings = data.get("embeddings") or []
        if len(embeddings) != len(inputs):
            raise RuntimeError(
                f"models embed count mismatch: got {len(embeddings)}, want {len(inputs)}"
            )
        return [assert_embedding_contract(vec, dim=self.cfg.expected_dim) for vec in embeddings]
