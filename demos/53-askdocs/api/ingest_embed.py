"""Embed chunk texts via Models and upsert into Memory (epic 53.03)."""

from __future__ import annotations

from typing import Any

from embeddings import EMBEDDING_DIM, EmbeddingContractError
from memory import MemoryClient
from models import ModelsClient
from store import Chunk, MessageStore


def embed_and_store_chunks(
    store: MessageStore,
    models: ModelsClient,
    memory: MemoryClient,
    document_id: str,
    chunks: list[Chunk],
    *,
    object_key: str = "",
) -> dict[str, Any]:
    """Embed chunks, upsert vectors, map memory_id, mark document ready.

    memory_id is the Memory record id, which equals the Postgres chunk id.
    """
    document_id = (document_id or "").strip()
    if not document_id:
        raise ValueError("document_id is required")
    memory.ensure_collection()
    if not chunks:
        store.mark_document_ready(document_id)
        return {"documentId": document_id, "upserted": 0, "chunks": 0}

    texts = [c.text for c in chunks]
    vectors = models.embed_batch(texts)
    if len(vectors) != len(chunks):
        raise RuntimeError("embed_batch size mismatch")

    records: list[dict[str, Any]] = []
    id_map: dict[str, str] = {}
    for chunk, vector in zip(chunks, vectors, strict=True):
        if len(vector) != EMBEDDING_DIM:
            raise EmbeddingContractError(
                f"chunk {chunk.id}: dim {len(vector)} != {EMBEDDING_DIM}"
            )
        memory_id = chunk.id
        id_map[chunk.id] = memory_id
        meta = {
            "document_id": chunk.document_id,
            "chunk_id": chunk.id,
            "ordinal": chunk.ordinal,
            "text": chunk.text,
        }
        rec: dict[str, Any] = {
            "id": memory_id,
            "vector": vector,
            "metadata": meta,
        }
        if object_key:
            rec["document_ref"] = object_key
        records.append(rec)

    upserted = memory.upsert(records)
    store.set_chunk_memory_ids(document_id, id_map)
    store.mark_document_ready(document_id)
    return {
        "documentId": document_id,
        "upserted": upserted,
        "chunks": len(chunks),
        "dim": EMBEDDING_DIM,
        "collection": memory.cfg.collection,
    }
