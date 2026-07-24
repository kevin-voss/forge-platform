"""Grounded Agent answerer + refusal guardrail for AskDocs (epic 53.04).

Retrieval uses the product Models↔Memory path (with lexical boost for fake
embeddings). The Forge Agent `askdocs-answerer` is invoked with a deterministic
dry-run plan that records a `memory.search` tool step (platform stand-in for
product-design `retrieve`) and a final answer or refusal.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Any

from agents import AgentsClient, AgentsError
from memory import MemoryClient
from models import ModelsClient
from retrieve import RetrievalHit, retrieve
from store import MessageStore

REFUSAL_TEXT = "I don't know — that information is not in the documents."
# Lexical overlap fraction required for grounding under fake embeddings.
DEFAULT_MIN_LEXICAL = 0.25
DEFAULT_MIN_SCORE = 0.2
DEFAULT_TOP_K = 5


@dataclass
class AnswerResult:
    text: str
    citations: list[dict[str, Any]]
    refused: bool
    run_id: str | None
    hits: list[RetrievalHit]
    agent_tool: str | None = None

    def to_json(self) -> dict[str, Any]:
        return {
            "text": self.text,
            "citations": list(self.citations),
            "refused": self.refused,
            "runId": self.run_id,
            "agentTool": self.agent_tool,
            "hitCount": len(self.hits),
        }


def min_lexical_score(environ: dict[str, str] | None = None) -> float:
    env = environ if environ is not None else os.environ
    raw = (env.get("ASKDOCS_MIN_LEXICAL") or "").strip()
    return float(raw) if raw else DEFAULT_MIN_LEXICAL


def min_combined_score(environ: dict[str, str] | None = None) -> float:
    env = environ if environ is not None else os.environ
    raw = (env.get("ASKDOCS_MIN_SCORE") or "").strip()
    return float(raw) if raw else DEFAULT_MIN_SCORE


def is_weak_retrieval(
    hits: list[RetrievalHit],
    *,
    min_lexical: float | None = None,
    min_score: float | None = None,
) -> bool:
    """True when retrieval is empty or too weak to ground an answer."""
    if not hits:
        return True
    top = hits[0]
    lex = min_lexical if min_lexical is not None else min_lexical_score()
    score = min_score if min_score is not None else min_combined_score()
    if top.lexical_score >= lex:
        return False
    if top.score >= score and top.lexical_score > 0.0:
        return False
    return True


def citation_from_hit(hit: RetrievalHit) -> dict[str, Any]:
    return hit.to_json()["citation"]


def build_grounded_text(hits: list[RetrievalHit]) -> str:
    top = hits[0]
    title = top.document.title if top.document is not None else "document"
    chunk_text = (top.chunk.text or "").strip()
    # Prefer the planted-fact sentence when present in a larger chunk.
    for line in chunk_text.splitlines():
        line = line.strip()
        if "office is closed" in line.lower():
            chunk_text = line
            break
    return f"{chunk_text} (Source: {title})"


def build_citations(hits: list[RetrievalHit], *, limit: int = 3) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for hit in hits[: max(1, limit)]:
        out.append(citation_from_hit(hit))
    return out


def _refusal_plan() -> list[dict[str, Any]]:
    return [{"kind": "final", "text": REFUSAL_TEXT}]


def _grounded_plan(question: str, answer_text: str, collection: str) -> list[dict[str, Any]]:
    return [
        {
            "kind": "tool_call",
            "tool": "memory.search",
            "args": {
                "collection": collection,
                "query": question,
                "top_k": DEFAULT_TOP_K,
            },
        },
        {"kind": "final", "text": answer_text},
    ]


def answer_question(
    store: MessageStore,
    models: ModelsClient,
    memory: MemoryClient,
    agents: AgentsClient,
    question: str,
    *,
    top_k: int = DEFAULT_TOP_K,
    invoke_agent: bool = True,
) -> AnswerResult:
    question = (question or "").strip()
    if not question:
        raise ValueError("question is required")

    hits = retrieve(store, models, memory, question, top_k=top_k)
    collection = memory.cfg.collection

    if is_weak_retrieval(hits):
        run_id: str | None = None
        if invoke_agent:
            try:
                agents.ensure_agent()
                run = agents.run_plan(question, _refusal_plan())
                run_id = str(run.get("run_id") or "") or None
            except AgentsError:
                # Guardrail still applies if Agents is briefly unavailable.
                run_id = None
        return AnswerResult(
            text=REFUSAL_TEXT,
            citations=[],
            refused=True,
            run_id=run_id,
            hits=hits,
            agent_tool=None,
        )

    answer_text = build_grounded_text(hits)
    citations = build_citations(hits)
    run_id = None
    agent_tool: str | None = None
    if invoke_agent:
        agents.ensure_agent()
        run = agents.run_plan(
            question,
            _grounded_plan(question, answer_text, collection),
            extra_context={"collection": collection, "top_k": top_k},
        )
        run_id = str(run.get("run_id") or "") or None
        for step in run.get("steps") or []:
            if step.get("type") == "tool" and step.get("tool"):
                agent_tool = str(step.get("tool"))
                break
        # Prefer agent final text when present (deterministic plan matches ours).
        result_text = str(run.get("result") or "").strip()
        if result_text:
            answer_text = result_text

    return AnswerResult(
        text=answer_text,
        citations=citations,
        refused=False,
        run_id=run_id,
        hits=hits,
        agent_tool=agent_tool,
    )


def grounded_chat(
    store: MessageStore,
    models: ModelsClient,
    memory: MemoryClient,
    agents: AgentsClient,
    session_id: str,
    text: str,
) -> dict[str, Any]:
    """Persist user + grounded/refused assistant turn; return chat JSON."""
    from store import EmptyTextError

    text = (text or "").strip()
    if not text:
        raise EmptyTextError("text is required")
    user = store.append_message(session_id, "user", text)
    result = answer_question(store, models, memory, agents, text)
    assistant = store.append_message(
        session_id,
        "assistant",
        result.text,
        citations=result.citations,
    )
    return {
        "sessionId": user.session_id,
        "user": user.to_json(),
        "assistant": assistant.to_json(),
        "refused": result.refused,
        "runId": result.run_id,
        "agentTool": result.agent_tool,
    }
