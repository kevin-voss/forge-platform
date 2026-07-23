# Demo projects & platform E2E verification

**Status:** Planning (no code yet). This directory holds the technical plans for a set of
small, real-world **demo products** that deploy onto the Forge Platform locally (Docker)
and are proven end-to-end with **visible browser automation**.

> This is the *verification* track. It builds nothing new in the platform. It exercises the
> shipped platform (epics `00`–`43`) the way a real customer would, finds where reality
> diverges from the specs, and records those divergences as **platform findings** — it never
> silently patches a service to make a demo pass.

---

## 1. Goal

One command:

```bash
make test-platform-e2e            # headed  — you watch the browser drive each product
make test-platform-e2e HEADLESS=1 # headless — same suite, CI-friendly, no window
```

brings up the platform, then for each demo product in order:

```text
deploy product onto Forge  →  seed data  →  run Playwright E2E (browser visible)
     →  assert product + platform behaviour  →  collect findings  →  tear down
```

At the end you get a **pass/fail per product**, a **service coverage report**, and — if any
platform behaviour was wrong — a populated [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md).

## 2. What "done" means

A demo product is **finished only if its full E2E suite passes**. When something fails, the
first question is always: *is this the demo's bug, or the platform's?*

| Failure cause | Action |
|---|---|
| Bug in the demo product (its Dockerfile, app code, manifest, test) | Fix it inside the demo. |
| Bug/limitation in a Forge **service** (Control, Identity, Events, …) | **Do not fix the service here.** Record a structured entry in [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md) using [`findings-template.md`](findings-template.md), then continue. |

The output of this whole track is therefore two things: **working demos**, and a **detailed
findings document** describing every real platform bug the demos surfaced.

## 3. The five demo products

Each is a small, believable product with a browser UI so the automation is meaningful. Together
they touch every platform service — see [`service-coverage-matrix.md`](service-coverage-matrix.md).

| # | Product | One-liner | Headline services exercised | Plan |
|---|---|---|---|---|
| 1 | **TaskFlow** | Team task manager with login | Identity (auth), managed Postgres, Secrets, Gateway, Build, Observe | [projects/01-taskflow.md](projects/01-taskflow.md) |
| 2 | **SnapNote** | Notes with file attachments + async processing | Storage (S3), Events/queue, Worker + node autoscaling | [projects/02-snapnote.md](projects/02-snapnote.md) |
| 3 | **AskDocs** | Upload docs, ask questions (RAG) | Models (embeddings), Memory (vectors), Agents, Storage | [projects/03-askdocs.md](projects/03-askdocs.md) |
| 4 | **OrderPipe** | Order-processing pipeline across services | Workflows, Events, Discovery, Network policy, multi-language | [projects/04-orderpipe.md](projects/04-orderpipe.md) |
| 5 | **PulseBoard** | Live metrics dashboard under load | Autoscaler (HTTP + node), Infrastructure, Observe | [projects/05-pulseboard.md](projects/05-pulseboard.md) |

Every product also implicitly exercises the baseline: **Control**, **CLI**, **Runtime**,
**Gateway**, **Build**, and the declarative **resource API** (`forge apply`).

## 4. How this plugs into the implementation system

This track is planned as **epics `50`–`56`** in the existing implementation system so it can be
built one step at a time with [`../implementation/IMPLEMENT_STEP.md`](../implementation/IMPLEMENT_STEP.md).

| Epic | Title | Steps (N) | Plan folder |
|---|---|---|---|
| [50](../implementation/epics/50-e2e-harness.md) | Platform E2E harness & orchestrator | `174`–`180` | [e2e-harness.md](e2e-harness.md) |
| [51](../implementation/epics/51-demo-taskflow.md) | Demo 1 — TaskFlow | `181`–`186` | [projects/01-taskflow.md](projects/01-taskflow.md) |
| [52](../implementation/epics/52-demo-snapnote.md) | Demo 2 — SnapNote | `187`–`192` | [projects/02-snapnote.md](projects/02-snapnote.md) |
| [53](../implementation/epics/53-demo-askdocs.md) | Demo 3 — AskDocs | `193`–`198` | [projects/03-askdocs.md](projects/03-askdocs.md) |
| [54](../implementation/epics/54-demo-orderpipe.md) | Demo 4 — OrderPipe | `199`–`205` | [projects/04-orderpipe.md](projects/04-orderpipe.md) |
| [55](../implementation/epics/55-demo-pulseboard.md) | Demo 5 — PulseBoard | `206`–`211` | [projects/05-pulseboard.md](projects/05-pulseboard.md) |
| [56](../implementation/epics/56-platform-e2e-gate.md) | Platform E2E gate & findings consolidation | `212`–`216` | this README + [PLATFORM_FINDINGS.md](PLATFORM_FINDINGS.md) |

### Implementation order (recommended)

Build **epic 50 first** (the harness everything depends on), then the demo products in the order
1→5 (each is independent and slots into the harness), then **epic 56** last (the aggregate gate).

> **Numbering note.** These steps continue the global **`N` queue** at **`174`–`216`**, right after
> the platform queue ends at `173` — so once the platform is built you keep going with `N = 174`.
> Resolve `N` → step via [`STEPS.md`](../implementation/STEPS.md#verification--demo-projects-queue-epics-5056)
> and implement with [`IMPLEMENT_STEP.md`](../implementation/IMPLEMENT_STEP.md). (Epics `26`–`43`,
> when materialized, receive `N` values continuing after `217`.)

## 5. Documents in this folder

| File | Purpose |
|---|---|
| [service-coverage-matrix.md](service-coverage-matrix.md) | Every service × which demo(s) exercise it, and how. The completeness contract. |
| [e2e-harness.md](e2e-harness.md) | Technical design of the harness, orchestrator, headed/headless, artifacts, findings hooks. |
| [findings-template.md](findings-template.md) | Copy-paste template for one platform finding. |
| [PLATFORM_FINDINGS.md](PLATFORM_FINDINGS.md) | The living, single findings document (starts empty). |
| [projects/](projects/) | One technical plan per demo product. |

## 6. Non-goals

* No production hosting — **local Docker only** for this track (targets `hetzner/aws/azure` are
  out of scope; the manifests stay portable so they *could* run there later).
* No new platform capabilities — if a demo needs something the platform can't do, that's a
  **finding**, not a feature to build here.
* Not a load/performance benchmark — autoscaling demos prove *behaviour* (replicas move), not SLOs.
