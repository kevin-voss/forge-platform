# Demo 17: Agent memory

End-to-end acceptance gate for epic 17 (Forge Memory). A standalone Compose
stack brings up `forge-memory` (durable volume), `forge-models`
(`FORGE_MODELS_BACKEND=fake`), and `forge-agents` (`FORGE_AGENTS_TOOLS_MODE=fake`).
Historical deployment incidents are embedded into a project collection; a new
failure with matching symptoms is queried via cosine NN; the `incident-memory`
agent calls `memory.search` and cites the retrieved incident in its diagnosis.
Project isolation and restart durability are asserted.

```text
1. seed historical incidents into proj-a/incidents (embedded via Models fake)
2. create a new failure with similar / matching symptoms
3. NN query returns the most similar historical incident(s) in correct order
4. agent uses memory.search and cites the retrieved incident in its diagnosis
5. proj-b sees none of proj-a's incidents (isolation)
6. restart forge-memory → vectors persist; query returns identical results
```

```text
acceptance.sh (host)
        │  HTTP
        ▼
forge-memory :4303 ──text upsert/query──► forge-models :4300 (fake embed)
        │ durable volume (vectors/ + meta/)
        │
forge-agents :4301 ──fake memory.search──► fixture (incident-db-timeout)
        │ dry_run FakeModelClient plan → cite incident id
```

## What this demo checks

* OpenAPI contracts for memory (+ agents/models) parse and document used paths.
* Incidents are seeded via the Models text-embed upsert path (`dim=384`).
* Cosine NN ordering is correct for the fixture set (text path + near-neighbor
  raw-vector path).
* `incident-memory` agent retrieves via `memory.search` and cites
  `incident-db-timeout` in the diagnosis.
* Project B cannot see project A collections/records (`404`, no existence leak).
* Restarting `forge-memory` preserves vectors; the same query returns identical
  ids and scores.
* Fixture-scale benchmark is documented (~27 ms @ N=10k, dim=32).
* Deterministic CI path: Models fake + Agents fake tools.

## Run

From the repository root:

```bash
make demo DEMO=17
```

Expect a final `demo 17 PASSED` line and exit code `0`. On failure the script
dumps memory/agents/models logs plus a memory query dump, then tears down with
`docker compose down -v`.

Optional bring-up only (leaves the stack running):

```bash
./demos/17-agent-memory/run.sh --phase=up
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_MEMORY_URL` | `http://127.0.0.1:4303` | Host memory API + readiness |
| `FORGE_AGENTS_URL` | `http://127.0.0.1:4301` | Host agents API + readiness |
| `FORGE_MODELS_URL` | `http://127.0.0.1:4300` | Host models API + readiness |
| `FORGE_AGENTS_TOOLS_MODE` | `fake` | Deterministic `memory.search` fixture |
| `FORGE_MODELS_BACKEND` | `fake` | Deterministic embeddings |
| `FORGE_MEMORY_PROJECT_A` | `proj-a` | Seed + query project |
| `FORGE_MEMORY_PROJECT_B` | `proj-b` | Isolation negative project |

## Fixtures

| File | Purpose |
|---|---|
| `fixtures/incidents.json` | Historical incidents + expected NN target |
| `fixtures/memory.search.json` | Agents fake `memory.search` results (ids match seed) |
| `agents/incident-memory.yaml` | Agent with `memory.search` + `memory:read` |

## Benchmark

Brute-force cosine at fixture scale is documented on the service README:

* **N = 10_000**, **dim = 32**, top_k=10 → **~27 ms** query latency (dev laptop)
* Reproduce: `cd services/forge-memory && cargo test --test bench_query_10k -- --nocapture`

No hard SLA this epic; numbers are for regression awareness.

## Security notes

* Dev auth (`X-Forge-Project`); project isolation asserted in acceptance.
* No secrets committed; Models/Agents fake modes only.
* Suitable for CI regression of embed → NN → agent citation → restart.
