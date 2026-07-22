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

## Two horizons

| Horizon | Epics | Where it is specified |
|---|---|---|
| **Shipping now** — self-hosted developer platform | `00`–`19` | [`specs.md`](../specs.md) + [`implementation/MASTER_PLAN.md`](implementation/MASTER_PLAN.md) |
| **Next** — standalone cloud on any provider | `20`–`43` | [`architecture/standalone-cloud.md`](architecture/standalone-cloud.md) + [`implementation/FUTURE_PLAN.md`](implementation/FUTURE_PLAN.md) |

Future work is strictly additive: steps `1`–`131` are frozen and nothing shipped is
rewritten — see [ADR 0007](decisions/0007-additive-evolution-after-epic-19.md).

Start here for the target platform:

* [Target architecture](architecture/standalone-cloud.md) — what Forge becomes
* [Resource model](concepts/resource-model.md) — how every capability is modelled
* [Application manifest](concepts/application-manifest.md) — what customers write
* [Autoscaling model](concepts/autoscaling-model.md) — how everything scales
* [Provider model](architecture/provider-model.md) — how any infrastructure plugs in
