# Epic 17: Forge Memory

## Status

In progress

## Goal

Provide a semantic vector-memory service (Rust, `services/forge-memory`, host port `4303`) with collections of fixed-dimension vectors + metadata, brute-force cosine nearest-neighbor search, persistent storage that survives restart, project namespaces with access control, and integration where Models produces embeddings and Agents retrieves memory. Proven by `demos/17-agent-memory`, where an agent retrieves similar historical incidents to inform a diagnosis.

## Why this epic exists

Agents become far more useful when they can recall similar past incidents, documentation, and context. `specs.md` Step 17 defines semantic storage/retrieval as the substrate for agent memory and product semantic search. Starting with correct, persistent brute-force cosine search (HNSW later) keeps it simple and verifiable.

## Primary code areas

* `services/forge-memory/` — Rust service (Axum)
* `contracts/openapi/forge-memory.openapi.yaml`
* `demos/17-agent-memory/`
* Integration seams: Models embed client; Agents retrieval tool

## Suggested language

Rust (per `specs.md` §4 / Step 17). Persistent store for vectors + metadata.

## Spec references

* `specs.md` → Step 17: Forge Memory (features, uses, integration, demo, tests, acceptance)
* `specs.md` → Step 14 (Models embeddings), Step 15 (Agents), Step 09 (Identity), Step 13 (Storage)

## Dependencies

* Epics `00`, `01` conventions
* Epic [`14-forge-models`](14-forge-models.md) for embeddings (minimum: `POST /embed` from `14.03`; fixed `embedding_dim`)
* Epic [`15-forge-agents`](15-forge-agents.md) for the retrieval tool (minimum: tool registry `15.03`, platform tools `15.05`)
* Epic [`09-forge-identity`](09-forge-identity.md) for project scope/ACL

## Out of scope for this epic

* HNSW / ANN index (documented as a later optimization; brute-force first)
* Hybrid lexical + semantic retrieval (later)
* Multi-node sharding/replication
* Producing embeddings itself (that is Models; Memory consumes/accepts vectors)

## Success demo

```bash
make demo DEMO=17
```

`demos/17-agent-memory`: historical deployment incidents are stored (embedded via Models); a new failure with similar symptoms is created; the agent retrieves related incidents from memory and cites them in its diagnosis.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [17.01](../steps/17-forge-memory/17.01-skeleton-persistence.md) | Skeleton + persistence | Complete | Rust, health, port 4303, persistence dir |
| [17.02](../steps/17-forge-memory/17.02-collections-vectors-metadata.md) | Collections + fixed-dim vectors + metadata | Not started | Depends on 17.01 |
| [17.03](../steps/17-forge-memory/17.03-upsert-cosine-nn.md) | Upsert + cosine NN query | Not started | Depends on 17.02; brute-force |
| [17.04](../steps/17-forge-memory/17.04-namespace-acl.md) | Namespace/ACL via Identity project scope | Not started | Depends on 17.03; 09 |
| [17.05](../steps/17-forge-memory/17.05-models-embed-agents-tool.md) | Models embed + Agents retrieval tool | Not started | Depends on 17.04; 14, 15 |
| [17.06](../steps/17-forge-memory/17.06-demo-and-gate.md) | Demo `17-agent-memory` + gate | Not started | Depends on 17.05 |

## Assumptions

* Service at `services/forge-memory/`, host port `4303`.
* Vectors persisted to a directory `FORGE_MEMORY_ROOT` (default `/data/memory`) via a durable volume; metadata index in embedded SQLite (mirroring storage epic self-containment).
* Each collection fixes its vector dimension (must match the producing model's `embedding_dim`); mismatched dimensions are rejected.
* Brute-force cosine over all vectors in the queried namespace is acceptable for the fixture datasets; a documented benchmark records latency at demo scale.
* Namespaces == project scope; ACL derived from Identity, with a `dev` header bypass until Identity enforced.

## Open questions

* Persistence format: append-only vector log + SQLite metadata vs a single embedded store. Assumption: raw vectors in a memory-mapped file + SQLite metadata; revisit if a mature Rust embedded vector store is standardized.
* Where embeddings are computed: caller passes vectors, or Memory calls Models. Assumption: support both — accept raw vectors AND an optional `embed` convenience that calls Models (17.05).
* Delete/GC of vectors: tombstone + compaction vs in-place. Assumption: tombstone in metadata, periodic/boot compaction.
* Benchmark target: define acceptable p95 at N=10k fixtures. Assumption: document measured numbers; no hard SLA this epic.

## Next step to implement

**[17.02](../steps/17-forge-memory/17.02-collections-vectors-metadata.md) — Collections + fixed-dim vectors + metadata**
