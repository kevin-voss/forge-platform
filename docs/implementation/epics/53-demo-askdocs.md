# Epic 53: Demo 3 — AskDocs (models + memory + agents RAG)

## Status

In progress

## Goal

A document Q&A product that proves the AI stack end-to-end: embed document chunks with **Models**,
store/retrieve them in **Memory**, and answer questions with a tool-using **Agent** grounded in the
uploaded docs — all deterministic via the platform's **fake** backends, verified by a headed
browser E2E where a planted fact is answered with a citation and an out-of-corpus question is
gracefully refused.

## Why this epic exists

Models + Memory + Agents is the platform's differentiating capability; AskDocs is the smallest RAG
product that makes all three interoperate on real content, offline and reproducibly. Full design:
[`../../demo-projects/projects/03-askdocs.md`](../../demo-projects/projects/03-askdocs.md).

## Primary code areas

* `demos/53-askdocs/` — API (Python) + chat SPA, ingest worker, resource + Agent docs, fixtures.
* `tests/e2e/projects/03-askdocs/spec.ts`.

## Suggested language

Python (API + ingest) + minimal chat SPA.

## Spec references

* `docs/demo-projects/projects/03-askdocs.md`
* Epics 14 (models), 17 (memory), 15 (agents), 13 (storage), 11 (events).

## Dependencies

* Epic **50** (harness) complete.
* Models, Memory, Agents, Storage available; fake backends supported (`FORGE_MODELS_BACKEND=fake`,
  `FORGE_AGENTS_TOOLS_MODE=fake`).

## Out of scope for this epic

* Real external LLM calls (determinism requires fakes).
* Multi-tenant document isolation beyond a single demo corpus.

## Success demo

`make demo DEMO=53`: upload a fixture handbook, ask "When is the office closed?" → answer contains
the planted fact with a citation; ask an out-of-corpus question → graceful "not in the documents";
chat history persists.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| 53.01 | Product scaffold + Postgres | Complete | API + chat SPA, documents/chunks/messages schema, baseline deploy |
| 53.02 | Storage upload + ingest pipeline | Not started | store doc to Storage; `document.uploaded` event → chunk worker |
| 53.03 | Embeddings (Models) + Memory upsert/query | Not started | fake embeddings; collection `askdocs-chunks`; kNN retrieval |
| 53.04 | Agent grounded answerer | Not started | Agent with `retrieve` tool; grounded answer + citations; refusal guardrail |
| 53.05 | E2E browser spec | Not started | upload→ready→ask→cited answer; out-of-corpus refusal; history persists |
| 53.06 | Demo + epic gate | Not started | `demos/53-askdocs`; `make demo DEMO=53`; wired into test-platform-e2e |

Ordering + `N`: [`../steps/53-demo-askdocs/README.md`](../steps/53-demo-askdocs/README.md).

## Open questions

* Exact embedding vector format/dimension contract between Models and Memory — pin in `53.03`;
  any mismatch is a finding (Models↔Memory contract).
