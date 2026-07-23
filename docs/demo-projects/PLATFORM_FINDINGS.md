# Platform findings

Single, living record of **platform bugs and contract mismatches** surfaced by the demo-project
E2E track. Populated while running the demos (epics 51–55) and consolidated by epic
[`56.03`](../implementation/steps/56-platform-e2e-gate/56.03-findings-consolidation.md).

**Rules**
* One entry per finding, using [`findings-template.md`](findings-template.md). Append-only; never
  edit a demo's *service* to make a finding disappear — fixing the platform is separate work.
* Only genuine **platform** issues go here. Demo/app/manifest/test bugs are fixed in the demo.
* The harness (`tests/e2e/harness/findings.ts`) is the automated writer; humans may add entries too.

Machine-readable mirror: `tests/e2e/artifacts/findings.json`.

---

## Summary

| Metric | Count |
|---|---|
| Total findings | 0 |
| Open | 0 |
| Blocker | 0 |
| Major | 0 |
| Minor | 0 |

## By service

| Service | Open | Blocker | Major | Minor |
|---|--:|--:|--:|--:|
| _(none yet)_ | 0 | 0 | 0 | 0 |

## By demo

| Demo | Findings |
|---|--:|
| 01-taskflow | 0 |
| 02-snapnote | 0 |
| 03-askdocs | 0 |
| 04-orderpipe | 0 |
| 05-pulseboard | 0 |

---

## Findings

_No findings recorded yet. The first demo run appends entries below._

<!--
Append entries here, newest last, using the block from findings-template.md, e.g.:

### F-001 — Queue redelivers acked messages after consumer restart
| Field | Value |
| Status | Open |
| Severity | blocker |
| Service | forge-events |
...
-->
