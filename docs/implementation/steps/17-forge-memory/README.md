# Steps for epic 17-forge-memory

Epic: [`../../epics/17-forge-memory.md`](../../epics/17-forge-memory.md) · Status: **Complete**

Semantic vector memory (Rust, `services/forge-memory`, host port `4303`, demo `demos/17-agent-memory`).

| Step | Title | Status | Depends on |
|---|---|---|---|
| [17.01](17.01-skeleton-persistence.md) | Skeleton + persistence | Complete | 00, 01 |
| [17.02](17.02-collections-vectors-metadata.md) | Collections + fixed-dim vectors + metadata | Complete | 17.01 |
| [17.03](17.03-upsert-cosine-nn.md) | Upsert + cosine NN query | Complete | 17.02 |
| [17.04](17.04-namespace-acl.md) | Namespace/ACL via Identity project scope | Complete | 17.03, 09 |
| [17.05](17.05-models-embed-agents-tool.md) | Models embed + Agents retrieval tool | Complete | 17.04, 14, 15 |
| [17.06](17.06-demo-and-gate.md) | Demo `17-agent-memory` + gate | Complete | 17.05 |

Epic gate: `make demo DEMO=17`.
