"""Query-time retrieval: Models embed + Memory kNN + lexical boost (epic 53.03).

Fake/hash embeddings are deterministic but not semantic. Lexical overlap ensures
the planted-fact chunk is retrieved for its matching natural-language question
while still exercising the Models↔Memory vector path.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any

from memory import MemoryClient, MemoryHit
from models import ModelsClient
from store import Chunk, Document, MessageStore

_TOKEN_RE = re.compile(r"[a-z0-9]+", re.IGNORECASE)
_STOP = frozenset(
    {
        "a",
        "an",
        "the",
        "is",
        "are",
        "was",
        "were",
        "be",
        "to",
        "of",
        "in",
        "on",
        "for",
        "and",
        "or",
        "when",
        "what",
        "where",
        "who",
        "how",
        "does",
        "do",
        "did",
        "with",
        "at",
        "by",
        "from",
    }
)


@dataclass
class RetrievalHit:
    chunk: Chunk
    document: Document | None
    score: float
    vector_score: float
    lexical_score: float
    memory_id: str

    def to_json(self) -> dict[str, Any]:
        citation: dict[str, Any] = {
            "chunkId": self.chunk.id,
            "documentId": self.chunk.document_id,
            "ordinal": self.chunk.ordinal,
            "memoryId": self.memory_id,
        }
        if self.document is not None:
            citation["title"] = self.document.title
            citation["objectKey"] = self.document.object_key
        return {
            "chunk": self.chunk.to_json(),
            "score": self.score,
            "vectorScore": self.vector_score,
            "lexicalScore": self.lexical_score,
            "citation": citation,
        }


def tokenize(text: str) -> set[str]:
    return {t for t in _TOKEN_RE.findall((text or "").lower()) if t and t not in _STOP and len(t) > 1}


def lexical_score(query: str, chunk_text: str) -> float:
    q = tokenize(query)
    if not q:
        return 0.0
    c = tokenize(chunk_text)
    if not c:
        return 0.0
    overlap = len(q & c)
    return overlap / float(len(q))


def retrieve(
    store: MessageStore,
    models: ModelsClient,
    memory: MemoryClient,
    question: str,
    *,
    top_k: int = 5,
) -> list[RetrievalHit]:
    question = (question or "").strip()
    if not question:
        raise ValueError("question is required")
    top_k = max(1, int(top_k))

    memory.ensure_collection()
    query_vec = models.embed(question)
    # Oversample vector neighbors so lexical merge has room.
    knn = memory.query(query_vec, top_k=max(top_k * 3, top_k))
    vector_by_id = {h.id: h for h in knn}

    # Candidate pool: kNN hits + all chunks from ready documents (lexical path).
    chunk_ids = {h.id for h in knn}
    ready_chunks = store.list_ready_chunks()
    for chunk in ready_chunks:
        chunk_ids.add(chunk.id)

    chunks = store.get_chunks_by_ids(sorted(chunk_ids))
    docs: dict[str, Document | None] = {}
    hits: list[RetrievalHit] = []
    for chunk in chunks:
        if chunk.document_id not in docs:
            docs[chunk.document_id] = store.get_document(chunk.document_id)
        mem_hit: MemoryHit | None = vector_by_id.get(chunk.memory_id or chunk.id)
        vscore = float(mem_hit.score) if mem_hit is not None else 0.0
        lscore = lexical_score(question, chunk.text)
        # Prefer strong lexical matches for fake-embedding demos; keep vector signal.
        combined = (0.35 * vscore) + (0.65 * lscore)
        if combined <= 0.0 and vscore <= 0.0:
            continue
        hits.append(
            RetrievalHit(
                chunk=chunk,
                document=docs[chunk.document_id],
                score=combined,
                vector_score=vscore,
                lexical_score=lscore,
                memory_id=chunk.memory_id or chunk.id,
            )
        )

    hits.sort(key=lambda h: (-h.score, -h.lexical_score, -h.vector_score, h.chunk.ordinal, h.chunk.id))
    return hits[:top_k]
