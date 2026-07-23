# E2E harness & orchestrator — technical plan

Design for the machinery that runs all demo products end-to-end with **visible browser
automation**, collects **platform findings**, and exposes a single entry point:

```bash
make test-platform-e2e            # headed, runs demos 1→5, then the aggregate gate
make test-platform-e2e HEADLESS=1 # headless (CI); PROJECTS=01,03 to subset
```

Implemented by **epic [50](../implementation/epics/50-e2e-harness.md)**. Every demo product
(epics 51–55) plugs into the contracts defined here.

---

## 1. Layout

```text
tests/e2e/
├── package.json                # Playwright + TS; pinned versions; no other runtime deps
├── playwright.config.ts        # projects, video/trace, headed/headless from env
├── tsconfig.json
├── harness/
│   ├── orchestrator.ts         # runs projects in order, aggregates, exit code
│   ├── platform.ts             # bring platform up (make dev), health preflight
│   ├── demo.ts                 # DemoProject lifecycle: up → ready → seed → test → down
│   ├── findings.ts             # structured findings API → PLATFORM_FINDINGS.md + json
│   ├── forge.ts                # thin wrapper around the `forge` CLI (deploy/apply/logs)
│   ├── gateway.ts              # host-header helpers for *.localhost routing
│   └── report.ts               # per-run markdown/HTML report + coverage rollup
├── projects/
│   ├── 01-taskflow/spec.ts     # Playwright specs (one folder per demo product)
│   ├── 02-snapnote/spec.ts
│   ├── 03-askdocs/spec.ts
│   ├── 04-orderpipe/spec.ts
│   └── 05-pulseboard/spec.ts
└── artifacts/                  # gitignored: videos, traces, screenshots, reports, findings.json
```

Demo **product source** (Dockerfiles, app code, manifests, per-demo compose, seed scripts) lives
under `demos/5X-<name>/` beside the existing demos — the harness references them, tests live in
`tests/e2e/projects/`.

## 2. The `DemoProject` contract

Each demo product ships a `demos/5X-<name>/demo.json` the harness reads:

```jsonc
{
  "id": "01-taskflow",
  "title": "TaskFlow",
  "epic": "51",
  "compose": "demos/51-taskflow/docker-compose.yml", // optional extra product infra
  "deploy": "demos/51-taskflow/run.sh",              // brings product to Ready on Forge
  "seed": "demos/51-taskflow/seed.sh",               // idempotent test data
  "hosts": [                                          // gateway hostnames to preflight
    { "host": "app.taskflow.localhost", "path": "/", "expect": 200 },
    { "host": "api.taskflow.localhost", "path": "/health/ready", "expect": 200 }
  ],
  "baseURL": "http://app.taskflow.localhost:4000",
  "spec": "tests/e2e/projects/01-taskflow/spec.ts",
  "services": ["control","cli","runtime","gateway","build","identity","secrets","postgres","observe"],
  "teardown": "demos/51-taskflow/run.sh --down"
}
```

`services` feeds the coverage report (epic 56.02). `deploy`/`seed`/`teardown` are the same scripts
`make demo DEMO=5X` uses, so a product can be run standalone or under the orchestrator.

## 3. Lifecycle (per product)

```text
1. platform preflight   → platform.ts: `make dev` if not up; wait all service /health
2. product up           → demo.deploy(): forge build (if source) + forge apply -f; wait Ready
3. seed                 → demo.seed(): create users/data; idempotent
4. host preflight       → gateway.ts: each demo.json host returns expected status
5. e2e                  → Playwright runs demo.spec with baseURL; video+trace recorded
6. assert               → product assertions + platform assertions (see §6)
7. findings             → any platform assertion failure → findings.ts (does NOT hard-stop suite)
8. teardown             → demo.teardown() unless KEEP=1
```

A product **passes** only when steps 4–6 all pass with **zero platform findings of severity
`blocker`** attributed to it. Non-blocker findings are recorded and the run continues.

## 4. Headed vs headless

| Mode | Trigger | Behaviour |
|---|---|---|
| Headed (default) | `make test-platform-e2e` | `headless:false`, `slowMo` ~250ms so a human can watch each step; window follows the active product. |
| Headless | `HEADLESS=1` or `CI=1` | `headless:true`, no `slowMo`; identical assertions; used in CI. |

Both modes always record **trace + video + screenshots-on-failure** into `tests/e2e/artifacts/`
so a headless CI failure is replayable locally (`npx playwright show-trace`).

## 5. Gateway hostname routing (key local detail)

Products are reached through the Gateway on `127.0.0.1:4000` using **Host-based routing**
(`app.taskflow.localhost`, `api.orderpipe.localhost`, …). Two facts make this work headlessly:

* Chromium/Firefox resolve any `*.localhost` name to loopback automatically (RFC 6761), so
  `http://app.taskflow.localhost:4000` reaches the Gateway with `Host: app.taskflow.localhost:4000`.
* The Gateway must match the route on the **hostname ignoring the `:4000` port**. Confirm this in
  `50.03`; if the Gateway 404s on a port-suffixed Host, that is a **platform finding**, and the
  documented fallback is to run Playwright behind a tiny host-rewriting proxy (kept in the harness,
  never in the product).

`curl` preflights use explicit `-H "Host: ..."` against `http://127.0.0.1:4000`.

## 6. Product vs platform assertions

Every spec separates two assertion kinds so failures are correctly attributed:

```ts
// product assertion — a bug here means the demo is wrong; fix the demo.
expect(page.getByRole('row', { name: 'Buy milk' })).toBeVisible();

// platform assertion — a bug here means Forge is wrong; record, do not fix the service.
await platform.expect('observe', async () => {
  const spans = await forge.traces({ service: 'taskflow-api', route: 'POST /tasks' });
  assert(spans.length > 0, 'no trace recorded for POST /tasks');
}); // on failure → findings.record({ service:'forge-observe', ... }) and mark product degraded
```

`platform.expect(service, fn)` wraps a platform-behaviour check: on throw it calls
`findings.record(...)` with the service, the demo, the expectation, and captured evidence
(HTTP status, response body, relevant log lines, trace ids), then decides blocker vs degraded per
a per-check severity.

## 7. Findings integration

`findings.ts` is the only writer to [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md) and a machine
-readable `artifacts/findings.json`. API:

```ts
findings.record({
  service: 'forge-events',
  demo: '02-snapnote',
  severity: 'blocker' | 'major' | 'minor',
  title: 'Queue redelivers acked messages after consumer restart',
  tested: '...what the demo did...',
  expected: '...per contracts/openapi/forge-events...',
  actual: '...what happened...',
  evidence: { httpStatus, body, logs, traceId, screenshot },
  repro: ['make demo DEMO=52', '...'],
});
```

Each record appends a section built from [`findings-template.md`](findings-template.md) and
increments per-service counters used by the final report. Findings are **append-only** across runs
(deduped by `service+title`); fixing a finding is a separate platform change outside this track.

## 8. `make test-platform-e2e` orchestration

`orchestrator.ts` (invoked by the Make target) does:

```text
parse env: HEADLESS, PROJECTS=01,02.. (default all), KEEP, FINDINGS_ONLY
platform preflight (fail fast if platform can't come up → single blocker finding)
for each selected product in numeric order:
    run lifecycle (§3); capture pass/degraded/fail + findings + artifacts
write run report (report.ts) + coverage rollup (§ matrix)
exit 0 iff: all selected products passed AND zero blocker findings
```

Make target (planned, added in `50.05` — not created in this planning pass):

```make
test-platform-e2e:
	@cd tests/e2e && npm ci --no-audit --no-fund
	@cd tests/e2e && HEADLESS=$(HEADLESS) PROJECTS=$(PROJECTS) KEEP=$(KEEP) node harness/orchestrator.js
```

Related targets: `make e2e-install` (Playwright browsers), `make e2e-report` (open last report),
`make demo DEMO=5X` (single product, reuses the same lifecycle).

## 9. Determinism

* AI backends use fakes: `FORGE_MODELS_BACKEND=fake`, `FORGE_AGENTS_TOOLS_MODE=fake` (same knobs the
  capstone uses) so AskDocs answers are deterministic.
* Autoscaling demos use fast eval (`FORGE_AUTOSCALER_EVAL_INTERVAL_MS=1000`) and assert **direction
  and bounds** (replicas increased and stayed within min/max), never exact counts or timings.
* Seeds are idempotent; specs create their own uniquely-named data and clean up.

## 10. CI mode

`CI=1` implies `HEADLESS=1`, `KEEP=0`, uploads `tests/e2e/artifacts/` (traces, videos, report,
`findings.json`, `PLATFORM_FINDINGS.md`). A `CI_SUBSET` variable can restrict to the fast products
(`PROJECTS=01,03`) for PR gating while nightly runs the full five, mirroring the capstone's
`CI_SUBSET` pattern.

## 11. Open questions

* ~~Do we standardize product frontends on one stack?~~ **Decided in 50.02:** one shared minimal
  SPA style (static HTML/CSS/vanilla JS + `fetch`) for all five products — see
  [`shared-spa-style.md`](shared-spa-style.md).
* Should findings optionally open Beads issues? Out of scope for planning; `findings.json` is
  structured enough to bridge later.
