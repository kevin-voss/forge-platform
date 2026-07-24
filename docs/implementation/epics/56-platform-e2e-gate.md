# Epic 56: Platform E2E gate & findings consolidation

## Status

In progress

## Goal

The aggregate gate. When done, `make test-platform-e2e` runs all five demo products in order
(headed and headless), verifies **every platform service was exercised** (coverage gate),
consolidates all discovered platform bugs into a single ranked
[`PLATFORM_FINDINGS.md`](../../demo-projects/PLATFORM_FINDINGS.md), and produces one run report.
This is the north-star of the verification track: "the whole platform, driven like a customer, in
one command."

## Why this epic exists

The individual demo epics prove their slice; this epic proves the **set** — ordering, coverage
completeness, a clean CI headless path, and a single authoritative findings artifact — so a red
result points precisely at the responsible service.

## Primary code areas

* `tests/e2e/harness/` — orchestrator aggregation, coverage check, consolidated report.
* `Makefile` — final `test-platform-e2e` behaviour (full suite + subsets).
* `docs/demo-projects/PLATFORM_FINDINGS.md` — consolidated, triaged output.

## Suggested language

TypeScript/Node (orchestrator) + markdown reporting.

## Spec references

* `docs/demo-projects/README.md`, `e2e-harness.md`, `service-coverage-matrix.md`.

## Dependencies

* Epics **50–55** complete (harness + all five demos).

## Out of scope for this epic

* Fixing any finding (findings are handed off as separate platform work).
* New products or services.

## Success demo

`make test-platform-e2e` (and `HEADLESS=1`) runs demos 1→5, prints per-product results + a coverage
table showing every service touched, exits 0 only when all products pass with zero blocker
findings, and leaves a consolidated report + `PLATFORM_FINDINGS.md`.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| 56.01 | Full-suite orchestration | Complete | default `PROJECTS` = 01–05; aggregate pass/degraded/fail + exit code |
| 56.02 | Coverage verification gate | Complete | `services.json` + coverage.ts; full suite fails on uncovered services |
| 56.03 | Findings consolidation + triage | Not started | dedupe, rank by severity, group by service; summary/by-service/by-demo tables |
| 56.04 | Headless/CI mode + artifacts | Not started | `HEADLESS=1`/`CI=1`, `CI_SUBSET`, upload traces/videos/report/findings.json |
| 56.05 | North-star gate + docs | Not started | final acceptance, runbook, README; `make test-platform-e2e` is the gate |

Ordering + `N`: [`../steps/56-platform-e2e-gate/README.md`](../steps/56-platform-e2e-gate/README.md).

## Open questions

* PR gate = fast subset (`PROJECTS=01,03`) vs full five nightly? Recommend subset on PR, full
  nightly (decide in `56.04`, mirroring the capstone `CI_SUBSET`).
