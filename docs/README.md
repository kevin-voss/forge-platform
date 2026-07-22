# Forge Platform documentation

| Area | Purpose |
|---|---|
| [architecture/](architecture/) | System shape, local topology, boundaries |
| [concepts/](concepts/) | Product principles and vocabulary |
| [contracts/](contracts/) | Shared API / event contract notes |
| [development/](development/) | Local workflow, repo layout, conventions |
| [operations/](operations/) | Ports, Make targets, day-2 ops |
| [testing/](testing/) | Test strategy and suites |
| [decisions/](decisions/) | Architecture Decision Records |
| [implementation/](implementation/) | Epics, atomic steps, progress, agent prompts |

Product vision and coarse roadmap live in [`specs.md`](../specs.md).

Implementation detail lives under [`implementation/`](implementation/):

* **Epics** = roadmap capabilities (often one service or cross-cutting concern)
* **Steps** = small shippable increments (a service usually has **many** steps)
