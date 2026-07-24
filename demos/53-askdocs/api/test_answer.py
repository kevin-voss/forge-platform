#!/usr/bin/env python3
"""Grounded answerer + refusal guardrail tests (epic 53.04)."""

from __future__ import annotations

import unittest
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from answer import (
    REFUSAL_TEXT,
    answer_question,
    build_grounded_text,
    is_weak_retrieval,
)
from chunking import chunk_text
from embeddings import EMBEDDING_DIM, EMBEDDING_MODEL, MEMORY_COLLECTION
from retrieve import RetrievalHit, retrieve
from store import Chunk, Document
from test_embeddings import FakeMemory, FakeModels, FakeStore, PLANTED_FACT, QUESTION

FIXTURE = Path(__file__).resolve().parents[1] / "fixtures" / "company-handbook.txt"
OUT_OF_CORPUS = "What is the CEO's home address?"


@dataclass
class FakeAgents:
    """Captures dry-run plans; returns a succeeded run shaped like forge-agents."""

    agent_name: str = "askdocs-answerer"
    calls: list[dict[str, Any]] = field(default_factory=list)
    raise_on_run: bool = False

    def ping(self) -> None:
        return None

    def ensure_agent(self) -> None:
        return None

    def run_plan(
        self,
        run_input: str,
        plan: list[dict[str, Any]],
        *,
        extra_context: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        if self.raise_on_run:
            from agents import AgentsError

            raise AgentsError("forced failure")
        self.calls.append(
            {
                "input": run_input,
                "plan": plan,
                "extra_context": dict(extra_context or {}),
            }
        )
        steps: list[dict[str, Any]] = []
        final_text = ""
        for item in plan:
            kind = item.get("kind")
            if kind == "tool_call":
                steps.append(
                    {
                        "type": "tool",
                        "tool": item.get("tool"),
                        "observation": {"ok": True, "results": []},
                    }
                )
            elif kind == "final":
                final_text = str(item.get("text") or "")
                steps.append({"type": "final", "observation": final_text})
        return {
            "run_id": f"run-{len(self.calls)}",
            "status": "succeeded",
            "result": final_text,
            "steps": steps,
        }


def _corpus() -> tuple[FakeStore, FakeModels, FakeMemory, list[RetrievalHit]]:
    texts = chunk_text(FIXTURE.read_text(encoding="utf-8"), max_chars=400)
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
    return store, models, memory, hits


class GuardrailTests(unittest.TestCase):
    def test_empty_hits_are_weak(self) -> None:
        self.assertTrue(is_weak_retrieval([]))

    def test_strong_lexical_is_not_weak(self) -> None:
        now = datetime.now(timezone.utc)
        chunk = Chunk(
            id="c1",
            document_id="d1",
            ordinal=0,
            text=PLANTED_FACT,
            memory_id="c1",
            created_at=now,
        )
        hit = RetrievalHit(
            chunk=chunk,
            document=None,
            score=0.5,
            vector_score=0.1,
            lexical_score=0.5,
            memory_id="c1",
        )
        self.assertFalse(is_weak_retrieval([hit]))


class AnswererTests(unittest.TestCase):
    def test_planted_fact_grounded_with_citation(self) -> None:
        self.assertTrue(FIXTURE.is_file())
        store, models, memory, _ = _corpus()
        agents = FakeAgents()
        result = answer_question(store, models, memory, agents, QUESTION)  # type: ignore[arg-type]
        self.assertFalse(result.refused)
        self.assertIn(PLANTED_FACT, result.text)
        self.assertTrue(result.citations)
        cite = result.citations[0]
        self.assertEqual(cite.get("title"), "Company Handbook")
        self.assertTrue(cite.get("chunkId"))
        self.assertTrue(cite.get("documentId"))
        self.assertEqual(len(agents.calls), 1)
        plan = agents.calls[0]["plan"]
        self.assertEqual(plan[0]["kind"], "tool_call")
        self.assertEqual(plan[0]["tool"], "memory.search")
        self.assertEqual(plan[0]["args"]["collection"], MEMORY_COLLECTION)
        self.assertEqual(plan[1]["kind"], "final")
        self.assertIn(PLANTED_FACT, plan[1]["text"])
        self.assertEqual(result.agent_tool, "memory.search")
        self.assertEqual(result.run_id, "run-1")

    def test_out_of_corpus_refused(self) -> None:
        store, models, memory, _ = _corpus()
        agents = FakeAgents()
        result = answer_question(store, models, memory, agents, OUT_OF_CORPUS)  # type: ignore[arg-type]
        self.assertTrue(result.refused)
        self.assertEqual(result.text, REFUSAL_TEXT)
        self.assertEqual(result.citations, [])
        self.assertNotIn("Acme", result.text)
        self.assertNotIn("office", result.text.lower())
        self.assertEqual(len(agents.calls), 1)
        plan = agents.calls[0]["plan"]
        self.assertEqual(plan, [{"kind": "final", "text": REFUSAL_TEXT}])

    def test_build_grounded_text_prefers_office_line(self) -> None:
        store, models, memory, hits = _corpus()
        del store, models, memory
        text = build_grounded_text(hits)
        self.assertIn(PLANTED_FACT, text)
        self.assertIn("Source: Company Handbook", text)

    def test_contract_constants(self) -> None:
        self.assertEqual(EMBEDDING_MODEL, "local-embed-small")
        self.assertEqual(EMBEDDING_DIM, 384)
        self.assertEqual(MEMORY_COLLECTION, "askdocs-chunks")
        self.assertIn("not in the documents", REFUSAL_TEXT)


if __name__ == "__main__":
    unittest.main()
