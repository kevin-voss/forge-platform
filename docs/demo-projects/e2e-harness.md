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

### 50.03 outcome — Gateway host-with-port matching

**Confirmed: Gateway matches Host ignoring `:4000`.** `forge-gateway` normalizes the request
host via `net.SplitHostPort` before route lookup (`services/forge-gateway/internal/routes/match.go`
→ `normalizeHost`; covered by `TestMatchStripsHostPort`). Browser URLs of the form
`http://*.localhost:4000` therefore work **directly** — no host-rewrite proxy is required for the
default local Gateway.

Harness helpers (epic 50.03):

* `tests/e2e/harness/platform.ts` — `preflight()` runs `make dev` when infra is down, then waits
  on the same endpoints as root `make wait` (via `scripts/wait-for-service.sh`); failures raise a
  single blocker stub for 50.04.
* `tests/e2e/harness/gateway.ts` — `fetchWithHost` / `preflightHosts` / `productBaseURL` /
  `verifyHostPortMatching`; `startHostRewriteProxy` remains available as the harness-only
  fallback if a future Gateway regression stops stripping the port.

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

### 50.04 outcome — findings collector

**Landed:** `tests/e2e/harness/findings.ts` is the only automated writer to
[`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md) and `artifacts/findings.json`.
`record({service, demo, severity, title, …})` appends a template block, updates summary /
by-service / by-demo counters, merges JSON, and dedupes by `service+title`.
`platform.expect(service, fn, {severity})` catches platform-assertion throws, records a finding
with captured evidence, and returns `failed` (blocker) or `degraded` (major/minor) without
hard-failing the suite — ordinary Playwright `expect` stays for product bugs.

API:

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

### 50.05 outcome — orchestrator + Make targets

**Landed:** `tests/e2e/harness/orchestrator.ts` discovers `demos/*/demo.json` (numeric order),
honors `HEADLESS`/`CI`, `PROJECTS=01,50…`, `KEEP`, and `FINDINGS_ONLY`, runs platform preflight
then each product lifecycle, writes `artifacts/orchestrator-result.json`, and exits 0 iff all
selected products passed with zero blocker findings.

### 50.06 outcome — run report + coverage rollup

**Landed:** `tests/e2e/harness/report.ts` writes `artifacts/report.md` + `artifacts/report.html`
after every orchestrator run (product results, durations, findings counts, video/trace links).
Coverage rollup unions each product’s `demo.json.services` against
[`service-coverage-matrix.md`](service-coverage-matrix.md) and marks covered/uncovered
(informational; enforcement is 56.02). `make e2e-report` opens the last HTML report.

```make
test-platform-e2e:
	@cd tests/e2e && npm ci --no-audit --no-fund
	@cd tests/e2e && npm run build
	@cd tests/e2e && HEADLESS=$(HEADLESS) PROJECTS=$(PROJECTS) KEEP=$(KEEP) FINDINGS_ONLY=$(FINDINGS_ONLY) \
		node harness/orchestrator.js

e2e-report:
	@cd tests/e2e && npm run build
	@cd tests/e2e && node harness/report.js --open
```

Related targets: `make e2e-install` (Playwright browsers), `make e2e-report`,
`make demo DEMO=5X` (when `demos/5X-*/demo.json` exists, reuses the orchestrator lifecycle).

### 50.07 outcome — harness self-test demo + gate

**Landed:** `demos/50-e2e-harness` is a minimal nginx “hello” product (`hello.localhost`) with
`demo.json` → orchestrator lifecycle. Spec `tests/e2e/projects/50-harness/spec.ts` clicks
**Say hello**, asserts **Hello, Forge**, records one deliberate `minor` sample finding via
`platform.expect`, then restores `PLATFORM_FINDINGS.md` / `findings.json` so the gate exits 0.
`make demo DEMO=50` (and `HEADLESS=1`) is the epic 50 acceptance gate.

### 51.06 outcome — TaskFlow demo + epic gate

**Landed:** `demos/51-taskflow/demo.json` (`id: 01-taskflow`) wires deploy/seed/hosts/spec/services/
teardown into the orchestrator. `make demo DEMO=51` (and `HEADLESS=1`) plus
`make test-platform-e2e PROJECTS=01` run the full lifecycle; Playwright
`tests/e2e/projects/01-taskflow/spec.ts` covers signup→login→tasks→role gating and soft platform
asserts. Orchestrator exit treats **degraded** (non-blocker findings) as success so recorded
platform gaps (`F-001`–`F-003`) do not fail the gate. Epic 51 marked Complete.

### 52.06 outcome — SnapNote demo + epic gate

**Landed:** `demos/52-snapnote/demo.json` (`id: 02-snapnote`) wires deploy/seed/hosts/spec/services/
teardown into the orchestrator. `make demo DEMO=52` (and `HEADLESS=1`) plus
`make test-platform-e2e PROJECTS=02` run the full lifecycle; Playwright
`tests/e2e/projects/02-snapnote/spec.ts` covers upload→async thumbnail→burst→workers
scale/drain and soft platform asserts (autoscaler queueDepth, events idempotency, storage
thumbnail). Non-blocker finding `F-005` (forge-events lacks queueDepth admin metrics; demo uses
`demo52-metrics` sidecar) does not fail the gate. Epic 52 marked Complete.

### 53.06 outcome — AskDocs demo + epic gate

**Landed:** `demos/53-askdocs/demo.json` (`id: 03-askdocs`) wires deploy/seed/hosts/spec/services/
teardown into the orchestrator. `make demo DEMO=53` (and `HEADLESS=1`) plus
`make test-platform-e2e PROJECTS=03` run the full lifecycle; Playwright
`tests/e2e/projects/03-askdocs/spec.ts` covers upload→ready→cited grounded answer→out-of-corpus
refusal→history persist and soft platform asserts (Models↔Memory dim, Memory top-k, Agent
`memory.search` run, Observe evidence). Non-blocker findings `F-006` (no `retrieve` /
Control-applied `kind: Agent`) and `F-007` (Observe AI-stack evidence) do not fail the gate.
Epic 53 marked Complete.

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
