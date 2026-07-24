# Platform E2E runbook

How to run the verification track’s **north-star gate**: one command that deploys and
browser-drives all five demo products, checks service coverage, and refreshes the consolidated
findings report.

```bash
make test-platform-e2e            # headed (watch the browser)
make test-platform-e2e HEADLESS=1 # headless / CI
```

Design detail: [`e2e-harness.md`](e2e-harness.md). Track overview: [`README.md`](README.md).

---

## 1. Prerequisites

| Requirement | Notes |
|---|---|
| Docker Desktop (or Engine) running | Local Compose platform only |
| Repo root Make targets work | `make setup` once; `make dev` if the platform is not already up |
| Node.js 20+ | Harness under `tests/e2e/` (`npm ci` runs via Make) |
| Playwright browsers | `make e2e-install` once (or after Playwright upgrades) |
| Free host ports | Gateway `4000`, Control `4001`, and the rest of the local stack — see [`../operations/ports.md`](../operations/ports.md) |
| Disk for artifacts | Traces/videos land under `tests/e2e/artifacts/` (gitignored) |

The orchestrator brings the platform up via `make dev` when preflight finds it down. Prefer a
healthy stack first (`make status` / `make wait`) so the suite spends time on demos, not boot.

Fake AI backends are used by default for AskDocs (`FORGE_MODELS_BACKEND=fake`,
`FORGE_AGENTS_TOOLS_MODE=fake`) — no cloud API keys required.

---

## 2. Run modes

| Mode | Command | Behaviour |
|---|---|---|
| **Headed (default)** | `make test-platform-e2e` | Chromium window visible; light `slowMo` so you can follow each product |
| **Headless** | `make test-platform-e2e HEADLESS=1` | Same assertions; no window; typical local CI-style run |
| **CI full (nightly)** | `CI=1 make test-platform-e2e` | Forces headless + `KEEP=0`; stages upload bundle under `artifacts/` |
| **CI PR subset** | `CI=1 CI_SUBSET=1 make test-platform-e2e` | Headless; `PROJECTS=01,03` (TaskFlow + AskDocs) |
| **Single product** | `make test-platform-e2e PROJECTS=01` | Or `02`…`05`; also `make demo DEMO=51`…`55` |
| **Keep products up** | `make test-platform-e2e KEEP=1` | Skips teardown (ignored when `CI=1`) |
| **Findings only** | `make test-platform-e2e FINDINGS_ONLY=1` | Deploy/seed/platform asserts; skips Playwright |

Default (no `PROJECTS`) runs **01 → 05** in order, then the **coverage gate** (all services in
[`tests/e2e/harness/services.json`](../../tests/e2e/harness/services.json) must be covered) and
**findings consolidation**. PulseBoard / full suite use a longer deploy timeout (`DEMO_TIMEOUT_MS`
defaults to `900000` when `05` is included).

Example GitHub Actions workflow: [`.github/workflows/platform-e2e.yml`](../../.github/workflows/platform-e2e.yml)
(PR subset + nightly full; uploads `tests/e2e/artifacts/`).

---

## 3. What “green” means

Exit **0** when:

1. Platform preflight succeeds.
2. Every selected product ends **passed** or **degraded** (non-blocker findings only).
3. **Zero blocker** findings for the run.
4. On a **full** suite (`01`–`05`): coverage is complete (**20/20** services).

Exit **non-zero** when a product **fails**, a **blocker** finding is recorded, platform preflight
fails, or the full-suite coverage gate finds an uncovered service.

Non-blocker findings in [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md) are expected hand-offs —
they do **not** fail the gate. Do not patch platform services inside this track to clear them.

Console end state looks like:

```text
[orchestrator] suite: 01-taskflow, …, 05-pulseboard
…
coverage: 20/20 services
aggregate: PASS …
```

---

## 4. Reading the report

After every run the harness writes:

| Artifact | Location |
|---|---|
| Markdown report | `tests/e2e/artifacts/report.md` |
| HTML report | `tests/e2e/artifacts/report.html` |
| Machine result | `tests/e2e/artifacts/orchestrator-result.json` |
| Findings JSON | `tests/e2e/artifacts/findings.json` |
| Consolidated findings | [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md) (+ copy under `artifacts/` when `CI=1`) |
| Traces / videos | `tests/e2e/artifacts/projects-…/` |

Open the last HTML report:

```bash
make e2e-report
```

Report sections:

* **Summary** — overall PASS/FAIL, findings counts, coverage `N/N`
* **Products** — per-demo outcome, duration, links to video/trace
* **Service coverage rollup** — each platform service × which demos claimed it
* **Findings** — counts by severity (full triage tables live in `PLATFORM_FINDINGS.md`)

---

## 5. Handling findings

| Cause | Action |
|---|---|
| Demo / Dockerfile / manifest / Playwright bug | Fix inside `demos/5X-*` or `tests/e2e/projects/` |
| Platform service bug or contract gap | **Do not fix the service here.** Ensure a structured entry in [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md) (template: [`findings-template.md`](findings-template.md)); continue the suite |

Automated writer: `tests/e2e/harness/findings.ts` (`platform.expect` in specs). Consolidation
(dedupe, rank blocker → major → minor, triage table) runs at the end of every suite
(`findings.consolidate()` / `node harness/findings.js --consolidate`).

Hand-off workflow:

1. Read **Triage** in `PLATFORM_FINDINGS.md` (service owner + suspected component).
2. Reproduce with the finding’s `repro` steps or `make demo DEMO=5X`.
3. File / schedule **platform** work outside this track (epics `00`–`43` / Beads).
4. Close the finding only after the platform fix lands and a re-run no longer records it.

---

## 6. Troubleshooting

| Symptom | Likely cause | What to try |
|---|---|---|
| Preflight fails fast | Platform not healthy | `make status`; `make wait`; `make logs SVC=forge-control`; restart with `make restart` |
| Gateway host 404 / wrong app | Route or Host mismatch | Confirm `*.localhost:4000` via Gateway; see e2e-harness §5 (port-stripped Host matching) |
| Product deploy timeout | Slow build / scale legs | Full suite / PulseBoard already use 900s; raise `DEMO_TIMEOUT_MS=…` if needed |
| Playwright browser missing | Browsers not installed | `make e2e-install` |
| Coverage gate fails (`uncovered: …`) | `demo.json.services` drift vs `services.json` | Align product `services` tokens with [`service-coverage-matrix.md`](service-coverage-matrix.md); do not weaken the gate |
| Headed window does not appear | Display / CI env | Use `HEADLESS=1`; headed needs a local GUI session |
| Flaky AskDocs answers | Non-fake model backend | Ensure `FORGE_MODELS_BACKEND=fake` and `FORGE_AGENTS_TOOLS_MODE=fake` |
| Want to inspect after failure | Teardown hid the product | Re-run with `KEEP=1` (local only); open traces via `npx playwright show-trace path/to/trace.zip` from `tests/e2e/` |
| CI artifacts empty | Run did not reach staging | Confirm `CI=1`; check `artifacts/upload-manifest.json` |

Per-product standalone debug:

```bash
make demo DEMO=51          # TaskFlow gate
make demo DEMO=52          # SnapNote
make demo DEMO=53          # AskDocs
make demo DEMO=54          # OrderPipe
make demo DEMO=55          # PulseBoard
make demo DEMO=50          # Harness self-test only
```

---

## 7. Related docs

* [`README.md`](README.md) — track goal and five products
* [`service-coverage-matrix.md`](service-coverage-matrix.md) — completeness contract
* [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md) — living findings + triage
* [`e2e-harness.md`](e2e-harness.md) — harness internals
* Epic [`56-platform-e2e-gate`](../implementation/epics/56-platform-e2e-gate.md) — north-star gate
