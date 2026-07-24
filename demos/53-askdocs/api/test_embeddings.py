#!/usr/bin/env python3
"""Deterministic embeddings + planted-chunk retrieval tests (epic 53.03)."""

from __future__ import annotations

import hashlib
import math
import random
import unittest
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Sequence

from chunking import chunk_text
from embeddings import (
    EMBEDDING_DIM,
    EMBEDDING_MODEL,
    MEMORY_COLLECTION,
    EmbeddingContractError,
    assert_embedding_contract,
    l2_norm,
)
from memory import MemoryHit
from retrieve import lexical_score, retrieve, tokenize
from store import Chunk, Document

PLANTED_FACT = "The office is closed on the first Monday of each month."
QUESTION = "When is the office closed?"
FIXTURE = Path(__file__).resolve().parents[1] / "fixtures" / "company-handbook.txt"


def _l2_normalize(vector: Sequence[float]) -> list[float]:
    norm = math.sqrt(sum(v * v for v in vector))
    if norm == 0.0:
        return [0.0 for _ in vector]
    return [v / norm for v in vector]


def deterministic_embed(text: str, dim: int = EMBEDDING_DIM) -> list[float]:
    """Mirror forge-models LocalEmbeddingAdapter fake embedder."""
    digest = hashlib.sha256(text.encode("utf-8")).digest()
    seed = int.from_bytes(digest[:8], byteorder="big", signed=False)
    rng = random.Random(seed)
    raw = [rng.gauss(0.0, 1.0) for _ in range(dim)]
    return _l2_normalize(raw)


@dataclass
class FakeModels:
    model: str = EMBEDDING_MODEL
    expected_dim: int = EMBEDDING_DIM
    base_url: str = "fake://models"
    calls: list[str] = field(default_factory=list)

    def ping(self) -> None:
        return None

    def embed(self, text: str) -> list[float]:
        self.calls.append(text)
        return assert_embedding_contract(deterministic_embed(text), dim=self.expected_dim)

    def embed_batch(self, texts: Sequence[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]


@dataclass
class FakeMemory:
    collection: str = MEMORY_COLLECTION
    dim: int = EMBEDDING_DIM
    base_url: str = "fake://memory"
    records: dict[str, dict[str, Any]] = field(default_factory=dict)

    @property
    def cfg(self) -> Any:
        return self

    def ping(self) -> None:
        return None

    def ensure_collection(self) -> None:
        return None

    def upsert(self, records: Sequence[dict[str, Any]]) -> int:
        for rec in records:
            vec = assert_embedding_contract(rec["vector"], dim=self.dim)
            self.records[str(rec["id"])] = {
                "vector": vec,
                "metadata": dict(rec.get("metadata") or {}),
            }
        return len(records)

    def query(self, vector: Sequence[float], top_k: int = 5) -> list[MemoryHit]:
        q = _l2_normalize(assert_embedding_contract(vector, dim=self.dim))
        scored: list[MemoryHit] = []
        for rid, rec in self.records.items():
            v = _l2_normalize(rec["vector"])
            score = sum(a * b for a, b in zip(q, v, strict=True))
            scored.append(MemoryHit(id=rid, score=float(score), metadata=dict(rec["metadata"])))
        scored.sort(key=lambda h: (-h.score, h.id))
        return scored[: max(1, int(top_k))]


class FakeStore:
    def __init__(self, chunks: list[Chunk], documents: dict[str, Document]) -> None:
        self._chunks = {c.id: c for c in chunks}
        self._documents = documents

    def list_ready_chunks(self) -> list[Chunk]:
        return [
            c
            for c in self._chunks.values()
            if self._documents.get(c.document_id)
            and self._documents[c.document_id].status == "ready"
        ]

    def get_chunks_by_ids(self, chunk_ids: list[str]) -> list[Chunk]:
        return [self._chunks[i] for i in chunk_ids if i in self._chunks]

    def get_document(self, document_id: str) -> Document | None:
        return self._documents.get(document_id)


class TestEmbeddingContract(unittest.TestCase):
    def test_pinned_constants(self) -> None:
        self.assertEqual(EMBEDDING_DIM, 384)
        self.assertEqual(EMBEDDING_MODEL, "local-embed-small")
        self.assertEqual(MEMORY_COLLECTION, "askdocs-chunks")

    def test_assert_embedding_contract_accepts_dim(self) -> None:
        vec = deterministic_embed("hello")
        out = assert_embedding_contract(vec)
        self.assertEqual(len(out), EMBEDDING_DIM)
        self.assertAlmostEqual(l2_norm(out), 1.0, places=5)

    def test_assert_embedding_contract_rejects_dim_mismatch(self) -> None:
        with self.assertRaises(EmbeddingContractError):
            assert_embedding_contract([0.1, 0.2, 0.3])

    def test_fake_embed_is_deterministic(self) -> None:
        a = deterministic_embed(PLANTED_FACT)
        b = deterministic_embed(PLANTED_FACT)
        self.assertEqual(a, b)
        self.assertNotEqual(a, deterministic_embed(QUESTION))


class TestPlantedChunkRetrieval(unittest.TestCase):
    def test_lexical_overlap_finds_planted_fact(self) -> None:
        self.assertGreater(lexical_score(QUESTION, PLANTED_FACT), 0.0)
        self.assertIn("office", tokenize(QUESTION) & tokenize(PLANTED_FACT))
        self.assertIn("closed", tokenize(QUESTION) & tokenize(PLANTED_FACT))

    def test_topk_contains_planted_chunk_for_matching_query(self) -> None:
        self.assertTrue(FIXTURE.is_file(), f"missing fixture {FIXTURE}")
        texts = chunk_text(FIXTURE.read_text(encoding="utf-8"), max_chars=400)
        self.assertTrue(any(PLANTED_FACT in t for t in texts), texts)

        now = datetime.now(timezone.utc)
        doc = Document(
            id="doc1",
            title="Company Handbook",
            object_key="documents/doc1/company-handbook.txt",
            status="ready",
            created_at=now,
        )
        chunks: list[Chunk] = []
        for i, text in enumerate(texts):
            cid = f"chunk-{i:02d}"
            chunks.append(
                Chunk(
                    id=cid,
                    document_id=doc.id,
                    ordinal=i,
                    text=text,
                    memory_id=cid,
                    created_at=now,
                )
            )

        models = FakeModels()
        memory = FakeMemory()
        # Ingest path: embed + upsert (same as worker).
        for chunk in chunks:
            memory.upsert(
                [
                    {
                        "id": chunk.id,
                        "vector": models.embed(chunk.text),
                        "metadata": {
                            "document_id": chunk.document_id,
                            "chunk_id": chunk.id,
                            "ordinal": chunk.ordinal,
                            "text": chunk.text,
                        },
                    }
                ]
            )

        store = FakeStore(chunks, {doc.id: doc})
        hits = retrieve(store, models, memory, QUESTION, top_k=3)
        self.assertGreaterEqual(len(hits), 1, hits)
        joined = "\n".join(h.chunk.text for h in hits)
        self.assertIn(PLANTED_FACT, joined)
        self.assertTrue(any(PLANTED_FACT in h.chunk.text for h in hits), hits)
        # Citation mapping present.
        cite = hits[0].to_json()["citation"]
        self.assertEqual(cite["documentId"], doc.id)
        self.assertEqual(cite["title"], doc.title)
        self.assertIn("chunkId", cite)
        self.assertIn("memoryId", cite)

    def test_exact_text_query_is_top_vector_neighbor(self) -> None:
        """Pure vector path: querying with the planted text itself ranks it #1."""
        models = FakeModels()
        memory = FakeMemory()
        planted_id = "planted"
        other_id = "other"
        memory.upsert(
            [
                {
                    "id": planted_id,
                    "vector": models.embed(PLANTED_FACT),
                    "metadata": {"text": PLANTED_FACT},
                },
                {
                    "id": other_id,
                    "vector": models.embed("Employees may work remotely up to two days."),
                    "metadata": {"text": "remote"},
                },
            ]
        )
        hits = memory.query(models.embed(PLANTED_FACT), top_k=2)
        self.assertEqual(hits[0].id, planted_id)
        self.assertGreater(hits[0].score, 0.99)


if __name__ == "__main__":
    unittest.main()
