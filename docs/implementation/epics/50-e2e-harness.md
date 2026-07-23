# Epic 50: Platform E2E harness & orchestrator

## Status

Planning

## Goal

When this epic is done, one command — `make test-platform-e2e` (headed) or
`make test-platform-e2e HEADLESS=1` (CI) — brings the platform up, runs each demo product through
a standard **deploy → seed → browser E2E → assert → collect findings → tear down** lifecycle, and
emits a per-product pass/degraded/fail result, a service-coverage rollup, and an appended
[`PLATFORM_FINDINGS.md`](../../demo-projects/PLATFORM_FINDINGS.md). The harness is the shared
foundation every demo epic (51–55) plugs into.

## Why this epic exists

The demo products need a common way to be launched, driven by a real browser (visibly for humans,
headlessly for CI), asserted, and — critically — a disciplined way to distinguish "the demo is
wrong" from "the platform is wrong" so platform bugs are **recorded, not silently patched**.
Building this once keeps all five demos small and consistent.

## Primary code areas

* `tests/e2e/` (new) — Playwright + TypeScript harness, per-product specs, artifacts.
* `Makefile` — `test-platform-e2e`, `e2e-install`, `e2e-report` targets.
* `docs/demo-projects/` — findings doc + templates (already drafted in planning).

## Suggested language

TypeScript (Playwright test runner) for specs + a thin Node/bash orchestration layer; reuses the
existing `forge` CLI and `make dev` for platform bring-up.

## Spec references

* `docs/demo-projects/e2e-harness.md` — full technical design (authoritative for this epic).
* `demos/09-full-platform` — existing capstone patterns (`CI_SUBSET`, fake backends, accept suite).

## Dependencies

* Platform epics `00`–`24` shipped (deploy path, gateway, identity, observe, autoscaler, infra).
* `forge` CLI (epic 03) and `forge apply` (epic 20) available.

## Out of scope for this epic

* Any demo product itself (that is epics 51–55).
* Non-local deploy targets (hetzner/aws/azure).
* Fixing any platform bug the harness later surfaces.

## Success demo

`demos/50-e2e-harness` — a trivial hello-world product that the harness deploys, opens in a browser
(clicks a button, asserts text), records a deliberately-injected sample finding, and tears down —
proving the whole lifecycle and the findings pipeline before any real product depends on it.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| 50.01 | Harness skeleton (Playwright + config + artifacts) | Not started | `tests/e2e/` layout, headed/headless from env, video+trace, pinned versions |
| 50.02 | `DemoProject` contract + lifecycle runner | Not started | `demo.json` schema; `demo.ts` up→ready→seed→test→down; shared SPA style decision |
| 50.03 | Platform preflight + gateway host routing | Not started | `make dev` bring-up, all-service health wait, `*.localhost:4000` Host matching |
| 50.04 | Findings collector | Not started | `findings.ts` → `PLATFORM_FINDINGS.md` + `findings.json`; severity; product vs platform assert wrapper |
| 50.05 | `make test-platform-e2e` orchestrator | Not started | run products in order, `HEADLESS`/`PROJECTS`/`KEEP`, aggregate exit code |
| 50.06 | Run report + coverage rollup | Not started | markdown/HTML report, coverage from `demo.json.services`, artifact linking |
| 50.07 | Harness self-test demo + gate | Not started | `demos/50-e2e-harness` hello-world proves lifecycle end-to-end; `make demo DEMO=50` |

Ordering + `N`: [`../steps/50-e2e-harness/README.md`](../steps/50-e2e-harness/README.md).

## Open questions

* One shared minimal SPA style for all products vs per-product choice (recommend shared; decide in
  `50.02`).
* Whether findings should also open Beads issues (out of scope; `findings.json` bridges later).
