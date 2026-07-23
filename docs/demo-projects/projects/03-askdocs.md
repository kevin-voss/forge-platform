# Demo 3 — AskDocs

**Epic:** [`53-demo-askdocs`](../../implementation/epics/53-demo-askdocs.md) · **Focus:** the AI
stack — **Models** (embeddings/completion), **Memory** (vector store), **Agents** (tool-using
answerer) — over documents in **Storage**.

A small **document Q&A** app (RAG). Upload a document, then ask questions about it in a chat UI;
the app answers **grounded in the document** with citations. Uses the platform's deterministic
**fake** model/agent backends so answers are reproducible in CI.

---

## 1. Why this product

This is the platform's AI value proposition end-to-end: embeddings → semantic memory → an agent
that retrieves and answers. It proves Models, Memory, and Agents interoperate on real content
without any external LLM dependency (fakes make it deterministic and offline).

## 2. Services exercised

| Service | How AskDocs uses it | Proven by |
|---|---|---|
| forge-storage | Uploaded documents stored as objects. | Doc retrievable; chunk source traceable. |
| forge-models | `FORGE_MODELS_BACKEND=fake` embeddings for chunks + query; a completion for the final answer. | Deterministic vectors; stable answer text. |
| forge-memory | Chunk embeddings written to a semantic collection; kNN retrieval at query time. | Top-k chunks returned for a query. |
| forge-agents | `FORGE_AGENTS_TOOLS_MODE=fake` agent with a `retrieve` tool → composes a grounded answer + citations. | Answer cites the uploaded doc. |
| forge-events (light) | Ingestion pipeline event: `document.uploaded` → chunk+embed worker. | Doc becomes queryable after ingest. |
| managed Postgres | Document + chunk metadata, chat history. | History persists. |
| gateway/build/control/runtime/observe | Baseline + telemetry (trace spans across models/memory/agents). | Cross-service trace on a query. |

## 3. Architecture

```text
Browser ──▶ Gateway :4000
  app.askdocs.localhost ─▶ askdocs-web (chat UI)
  api.askdocs.localhost ─▶ askdocs-api (Python)
     Ingest:  store doc ─▶ Storage; publish document.uploaded ─▶ Events
              worker: chunk → forge-models embed → forge-memory upsert
     Ask:     embed query ─▶ forge-models
              retrieve top-k ─▶ forge-memory
              answer ─▶ forge-agents (agent: retrieve tool + completion) → grounded reply + citations
```

## 4. Manifests (illustrative — `53.02`–`53.04`)

```yaml
kind: Application            # askdocs-api
spec:
  env:
    - { name: FORGE_MODELS_URL,  value: http://forge-models.svc.forge:8080 }
    - { name: FORGE_MEMORY_URL,  value: http://forge-memory.svc.forge:8080 }
    - { name: FORGE_AGENTS_URL,  value: http://forge-agents.svc.forge:8080 }
    - { name: FORGE_MODELS_BACKEND, value: fake }
    - { name: FORGE_AGENTS_TOOLS_MODE, value: fake }
  dependencies:
    storage:  { type: object, bucket: askdocs-corpus }
    database: { type: postgres, name: askdocs-db }
---
kind: Agent                  # deterministic answerer
metadata: { name: askdocs-answerer, project: askdocs }
spec:
  tools: [ { name: retrieve, memory: askdocs-chunks } ]
  model: { backend: fake }
```

## 5. Data model

```text
documents(id, title, object_key, status[ingesting|ready], created_at)
chunks(id, document_id → documents.id, ordinal, text, memory_id)     # memory_id = vector id
messages(id, session_id, role[user|assistant], text, citations jsonb, created_at)
```

Memory collection `askdocs-chunks` holds the vectors; Postgres holds the human-readable mirror
and citation mapping.

## 6. E2E scenario (`tests/e2e/projects/03-askdocs/spec.ts`)

1. Open `app.askdocs.localhost`.
2. **Upload** a known fixture document (e.g. a short "Company Handbook" with a planted fact:
   *"The office is closed on the first Monday of each month."*). Wait for status `ready`.
3. **Ask** "When is the office closed?" in the chat.
4. Assert the answer **contains the planted fact** and shows a **citation** back to the handbook
   (deterministic because backends are fake and the fixture is fixed).
5. **Ask an unanswerable question** ("What is the CEO's home address?") → app answers with an
   "I don't know / not in the documents" style response (grounding guardrail), not a hallucination.
6. Reload → **chat history persists** (Postgres).

### Platform assertions (→ findings)
* Embedding dimensionality/format from Models matches what Memory expects on upsert and query.
* Memory kNN returns the planted chunk in top-k for the matching query.
* Agent invokes the `retrieve` tool (visible in agent run trace) and the answer is grounded in the
  retrieved chunk, not invented.
* A single query produces a connected trace spanning api→models→memory→agents in Observe.

## 7. Likely findings hotspots

Embedding/format contract drift between Models and Memory, memory collection lifecycle
(create/upsert/query), agent tool-invocation contract, determinism of the fake backends,
retrieval relevance thresholds.

## 8. Acceptance criteria

* `make demo DEMO=53` + `03-askdocs` E2E pass headed and headless, deterministically.
* Grounded answer with citation for the planted fact; graceful "don't know" for out-of-corpus.
* Ingest pipeline moves a document `ingesting`→`ready`; chat history persists.
* Zero blocker findings attributed to AskDocs.

## 9. Steps → see epic

`53.01` scaffold+db · `53.02` storage + ingest pipeline (events) · `53.03` Models embeddings +
Memory upsert/query · `53.04` Agents grounded answerer · `53.05` E2E browser spec · `53.06`
demo + gate. Details: [epic 53](../../implementation/epics/53-demo-askdocs.md).
