import { expect, test, type Page } from '@playwright/test';
import { execFile, spawn, type ChildProcess } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';

import { platform } from '../../harness/findings';

const execFileAsync = promisify(execFile);

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://board.pulseboard.localhost:4000';
const API_URL =
  process.env.FORGE_E2E_API_URL ?? 'http://api.pulseboard.localhost:4000';
const AUTOSCALER_URL =
  process.env.FORGE_AUTOSCALER_URL ?? 'http://127.0.0.1:4112';
const CONTROL_URL = process.env.FORGE_CONTROL_URL ?? 'http://127.0.0.1:4001';
const METRICS_URL =
  process.env.FORGE_DEMO55_METRICS_URL ?? 'http://127.0.0.1:4197';
const PROMETHEUS_URL =
  process.env.FORGE_PROMETHEUS_URL ?? 'http://127.0.0.1:3001';
const GRAFANA_URL = process.env.FORGE_GRAFANA_URL ?? 'http://127.0.0.1:3000';
const GATEWAY_URL = process.env.FORGE_GATEWAY_URL ?? 'http://127.0.0.1:4000';
const ENV_NAME = process.env.FORGE_ENVIRONMENT ?? 'local';
const API_NAME = 'pulseboard-api';
const API_POLICY = 'pulseboard-api-http';
const POOL_NAME = 'pulseboard-pool';
const MIN_REPLICAS = 1;
const MAX_REPLICAS = 10;
const MIN_NODES = 2;
const MAX_NODES = 3;
const TARGET_RPS = 50;
const LOAD_RPS = Number(process.env.PULSEBOARD_E2E_LOAD_RPS ?? 250);
const IDLE_RPS = Number(process.env.PULSEBOARD_E2E_IDLE_RPS ?? 20);
const OBSERVE_TOLERANCE = Number(process.env.OBSERVE_REPLICA_TOLERANCE ?? 0.5);
/** Node capacity leg — optional/thresholded for CI speed (`0` skips). */
const NODE_LEG =
  (process.env.PULSEBOARD_E2E_NODE_LEG ??
    (process.env.HEADLESS === '1' || process.env.CI === '1' ? '0' : '1')) ===
  '1';
const DEMO_ID = '05-pulseboard';

const repoRoot = path.resolve(__dirname, '../../../..');
const demoDir = path.join(repoRoot, 'demos/55-pulseboard');
const statePath = path.join(demoDir, '.demo-state');
const loadgenScript = path.join(demoDir, 'scripts/loadgen.sh');
const loadgenPidFile = path.join(demoDir, '.loadgen.pid');

function peakMinReplicas(rps: number): number {
  return Math.min(
    MAX_REPLICAS,
    Math.max(MIN_REPLICAS, Math.ceil(rps / TARGET_RPS)),
  );
}

function readDemoState(): Record<string, string> {
  if (!fs.existsSync(statePath)) {
    throw new Error(
      `missing ${statePath}; deploy PulseBoard (demos/55-pulseboard/run.sh) first`,
    );
  }
  const out: Record<string, string> = {};
  for (const line of fs.readFileSync(statePath, 'utf8').split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const eq = trimmed.indexOf('=');
    if (eq <= 0) continue;
    out[trimmed.slice(0, eq)] = trimmed.slice(eq + 1);
  }
  return out;
}

async function fetchJson(
  url: string,
  init: RequestInit = {},
  timeoutMs = 8_000,
): Promise<{ ok: boolean; status: number; body: unknown; text: string }> {
  const res = await fetch(url, {
    ...init,
    signal: AbortSignal.timeout(timeoutMs),
  });
  const text = await res.text();
  let body: unknown = undefined;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = text;
    }
  }
  return { ok: res.ok, status: res.status, body, text };
}

async function apiStats(): Promise<{
  replicas: number;
  rps: number;
  p95Ms: number;
  source?: string;
}> {
  const res = await fetchJson(`${API_URL}/stats`);
  if (!res.ok) throw new Error(`GET /stats HTTP ${res.status}`);
  const body = res.body as {
    replicas?: number;
    rps?: number;
    p95Ms?: number;
    source?: string;
  };
  return {
    replicas: Number(body.replicas ?? 0),
    rps: Number(body.rps ?? 0),
    p95Ms: Number(body.p95Ms ?? 0),
    source: body.source,
  };
}

async function fetchScalingPolicy(projectSlug: string): Promise<{
  desiredReplicas: number;
  minReplicas: number;
  maxReplicas: number;
  metricType: string;
  metricValue: number | null;
}> {
  const res = await fetchJson(
    `${AUTOSCALER_URL}/v1/projects/${projectSlug}/environments/${ENV_NAME}/scalingpolicies/${API_POLICY}`,
  );
  if (!res.ok) throw new Error(`GET ScalingPolicy HTTP ${res.status}`);
  const body = res.body as {
    spec?: { minReplicas?: number; maxReplicas?: number };
    status?: {
      desiredReplicas?: number;
      lastRecommendation?: { metricType?: string; metricValue?: number | null };
    };
  };
  return {
    desiredReplicas: Number(body.status?.desiredReplicas ?? 0),
    minReplicas: Number(body.spec?.minReplicas ?? MIN_REPLICAS),
    maxReplicas: Number(body.spec?.maxReplicas ?? MAX_REPLICAS),
    metricType: String(body.status?.lastRecommendation?.metricType ?? ''),
    metricValue:
      body.status?.lastRecommendation?.metricValue === undefined ||
      body.status?.lastRecommendation?.metricValue === null
        ? null
        : Number(body.status.lastRecommendation.metricValue),
  };
}

async function waitPolicyDesiredGe(
  projectSlug: string,
  min: number,
  timeoutMs = 120_000,
): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = 0;
  while (Date.now() < deadline) {
    const pol = await fetchScalingPolicy(projectSlug);
    last = pol.desiredReplicas;
    if (last >= min) return last;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(
    `ScalingPolicy desiredReplicas never reached ${min} (last=${last})`,
  );
}

async function waitPolicyDesiredEq(
  projectSlug: string,
  want: number,
  timeoutMs = 120_000,
): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = -1;
  while (Date.now() < deadline) {
    const pol = await fetchScalingPolicy(projectSlug);
    last = pol.desiredReplicas;
    if (last === want) return last;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(
    `ScalingPolicy desiredReplicas never == ${want} (last=${last})`,
  );
}

async function readUiReplicas(page: Page): Promise<number> {
  const text = ((await page.locator('#replicas').textContent()) || '').trim();
  const n = Number(text);
  if (!Number.isFinite(n)) {
    throw new Error(`dashboard #replicas not numeric: ${text}`);
  }
  return n;
}

async function waitUiReplicasGe(
  page: Page,
  min: number,
  timeoutMs = 180_000,
): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = 0;
  while (Date.now() < deadline) {
    last = await readUiReplicas(page);
    if (last >= min) return last;
    await page.waitForTimeout(1000);
  }
  throw new Error(`dashboard replicas never reached ${min} (last=${last})`);
}

async function waitUiReplicasEq(
  page: Page,
  want: number,
  timeoutMs = 180_000,
): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = -1;
  while (Date.now() < deadline) {
    last = await readUiReplicas(page);
    if (last === want) return last;
    await page.waitForTimeout(1000);
  }
  throw new Error(`dashboard replicas never == ${want} (last=${last})`);
}

async function queryObserveReplicas(): Promise<number> {
  const q = `sum(forge_replicas_ready{application="${API_NAME}"})`;
  const url = new URL(`${METRICS_URL}/api/v1/query`);
  url.searchParams.set('query', q);
  const res = await fetchJson(url.toString());
  if (!res.ok) throw new Error(`Observe query HTTP ${res.status}`);
  const body = res.body as {
    data?: { result?: Array<{ value?: [number, string] }> };
  };
  const raw = body.data?.result?.[0]?.value?.[1];
  if (raw === undefined) throw new Error('Observe replica query empty');
  return Number(raw);
}

async function queryPrometheusReplicas(): Promise<number | null> {
  try {
    const healthy = await fetchJson(`${PROMETHEUS_URL}/-/healthy`, {}, 5_000);
    if (!healthy.ok) return null;
    const q = `sum(forge_replicas_ready{application="${API_NAME}"})`;
    const url = new URL(`${PROMETHEUS_URL}/api/v1/query`);
    url.searchParams.set('query', q);
    const res = await fetchJson(url.toString(), {}, 8_000);
    if (!res.ok) return null;
    const body = res.body as {
      data?: { result?: Array<{ value?: [number, string] }> };
    };
    const raw = body.data?.result?.[0]?.value?.[1];
    return raw === undefined ? null : Number(raw);
  } catch {
    return null;
  }
}

async function countReadyForgeNodes(): Promise<number> {
  const res = await fetchJson(`${CONTROL_URL}/v1/forgenodes`);
  if (!res.ok) throw new Error(`GET /v1/forgenodes HTTP ${res.status}`);
  const body = res.body as {
    items?: Array<{ status?: { phase?: string } | string }>;
  };
  const items = Array.isArray(body)
    ? (body as Array<{ status?: { phase?: string } | string }>)
    : (body.items ?? []);
  return items.filter((n) => {
    const st = n.status;
    if (typeof st === 'string') return st.toLowerCase() === 'ready';
    return String(st?.phase ?? '').toLowerCase() === 'ready';
  }).length;
}

async function waitReadyNodesGe(
  min: number,
  timeoutMs = 180_000,
): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = 0;
  while (Date.now() < deadline) {
    last = await countReadyForgeNodes();
    if (last >= min) return last;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`readyNodes never reached ${min} (last=${last})`);
}

async function waitReadyNodesEq(
  want: number,
  timeoutMs = 240_000,
): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = -1;
  while (Date.now() < deadline) {
    last = await countReadyForgeNodes();
    if (last === want) return last;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`readyNodes never == ${want} (last=${last})`);
}

async function deploymentDesired(deploymentId: string): Promise<number> {
  const res = await fetchJson(`${CONTROL_URL}/v1/deployments/${deploymentId}`);
  if (!res.ok) throw new Error(`GET deployment HTTP ${res.status}`);
  const body = res.body as { desiredReplicas?: number };
  return Number(body.desiredReplicas ?? 0);
}

async function setIdleMetrics(rps: number, replicas: number): Promise<void> {
  const res = await fetchJson(`${METRICS_URL}/demo/application/${API_NAME}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      requestsPerSecond: rps,
      activeConnections: rps,
      sampleCount: 2000,
      p95LatencySeconds: 0.02,
      replicas,
    }),
  });
  if (!res.ok) throw new Error(`set idle metrics HTTP ${res.status}`);
}

async function patchApplicationDesired(
  projectSlug: string,
  desired: number,
): Promise<void> {
  const res = await fetchJson(
    `${CONTROL_URL}/v1/projects/${projectSlug}/environments/${ENV_NAME}/applications/${API_NAME}`,
    {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        spec: {
          scaling: {
            desiredReplicas: desired,
            minReplicas: MIN_REPLICAS,
            maxReplicas: MAX_REPLICAS,
          },
        },
      }),
    },
  );
  if (!res.ok) {
    throw new Error(`PATCH Application desiredReplicas HTTP ${res.status}: ${res.text}`);
  }
}

async function waitApiReplicasGe(min: number, timeoutMs = 120_000): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = 0;
  while (Date.now() < deadline) {
    last = (await apiStats()).replicas;
    if (last >= min) return last;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`/stats replicas never reached ${min} (last=${last})`);
}

async function waitApiReplicasEq(want: number, timeoutMs = 120_000): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = -1;
  while (Date.now() < deadline) {
    last = (await apiStats()).replicas;
    if (last === want) return last;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`/stats replicas never == ${want} (last=${last})`);
}

function logStep(msg: string): void {
  // eslint-disable-next-line no-console
  console.log(`[05-pulseboard] ${msg}`);
}

async function loadgen(
  cmd: 'start' | 'stop',
  rps?: number,
): Promise<void> {
  const args = [loadgenScript, cmd];
  if (cmd === 'start' && rps !== undefined) {
    args.push('--rps', String(rps));
  }
  // stdio ignore + short timeout: start backgrounds a long-lived worker; we
  // must not wait on its pipes (loadgen.sh also redirects them to .loadgen.log).
  await execFileAsync('bash', args, {
    encoding: 'utf8',
    timeout: 15_000,
    env: {
      ...process.env,
      GATEWAY_URL,
      API_HOST: 'api.pulseboard.localhost',
      METRICS_URL,
      APPLICATION: API_NAME,
      LOADGEN_PID_FILE: loadgenPidFile,
      PUBLISH_METRICS: '1',
    },
  });
}

/**
 * Bridge Application.spec.scaling.desiredReplicas → Deployment + Observe
 * replica gauge (same loop run.sh starts during the demo gate).
 */
function startSyncLoop(
  projectSlug: string,
  deploymentId: string,
): ChildProcess {
  const code = `
import json, time, urllib.request, sys
base, project, env, app, dep_id, metrics = sys.argv[1:7]
app_url = f"{base}/v1/projects/{project}/environments/{env}/applications/{app}"
dep_url = f"{base}/v1/deployments/{dep_id}"
metrics_url = f"{metrics.rstrip('/')}/demo/application/{app}"

def get(url):
    with urllib.request.urlopen(url, timeout=5) as r:
        return json.load(r)

def patch(url, body):
    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method="PATCH",
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return r.status

def put_metrics(replicas):
    data = json.dumps({"replicas": int(replicas)}).encode()
    req = urllib.request.Request(metrics_url, data=data, method="PUT",
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return r.status

while True:
    try:
        app_body = get(app_url)
        desired = (((app_body.get("spec") or {}).get("scaling") or {}).get("desiredReplicas"))
        if desired is None:
            time.sleep(1)
            continue
        desired = int(desired)
        dep = get(dep_url)
        cur = int(dep.get("desiredReplicas") or 0)
        if cur != desired:
            patch(dep_url, {"desiredReplicas": desired})
        put_metrics(desired)
    except Exception as exc:
        print(f"sync: {exc}", flush=True)
    time.sleep(1)
`;
  const child = spawn(
    'python3',
    ['-c', code, CONTROL_URL, projectSlug, ENV_NAME, API_NAME, deploymentId, METRICS_URL],
    { stdio: 'ignore', detached: false },
  );
  return child;
}

function stopChild(child: ChildProcess | undefined): void {
  if (!child || child.killed || child.exitCode !== null) return;
  try {
    child.kill('SIGTERM');
  } catch {
    /* ignore */
  }
}

/**
 * PulseBoard browser E2E (55.05): open dashboard → start load → replicas climb
 * (UI + Observe/Grafana) within bounds → optional node leg → stop load →
 * scale down. Soft platform.expect for autoscaler / control / observe / infra.
 */
test.describe('05-pulseboard', () => {
  test.describe.configure({ mode: 'serial' });

  test('load → replicas up (UI+Observe) → optional nodes → scale down', async ({
    page,
  }) => {
    // Node drain underutilization window + Ready waits can exceed 10m.
    test.setTimeout(NODE_LEG ? 900_000 : 600_000);

    const state = readDemoState();
    const projectSlug = state.PROJECT_SLUG;
    if (!projectSlug) throw new Error('PROJECT_SLUG missing from .demo-state');
    const apiDeploymentId = state.API_DEPLOYMENT_ID;
    if (!apiDeploymentId) {
      throw new Error('API_DEPLOYMENT_ID missing from .demo-state');
    }

    const wantPeak = peakMinReplicas(LOAD_RPS);
    let sync: ChildProcess | undefined;
    let upDesired = 0;
    let uiPeak = 0;
    let nodesBefore = MIN_NODES;
    let nodesAfterUp = MIN_NODES;

    try {
      // Ensure no leftover loadgen from a prior aborted run.
      await loadgen('stop').catch(() => undefined);

      sync = startSyncLoop(projectSlug, apiDeploymentId);
      logStep('sync started; forcing idle baseline');
      await setIdleMetrics(IDLE_RPS, MIN_REPLICAS);
      await patchApplicationDesired(projectSlug, MIN_REPLICAS).catch(() => undefined);
      await waitPolicyDesiredEq(projectSlug, MIN_REPLICAS, 90_000);
      await waitApiReplicasEq(MIN_REPLICAS, 90_000);

      // --- Product: dashboard baseline ---
      logStep('opening dashboard');
      await page.goto(BASE_URL);
      await expect(page.getByText('PulseBoard', { exact: true })).toBeVisible();
      await expect(page.getByRole('heading', { name: /live metrics/i })).toBeVisible();
      await expect(page.locator('#replicas')).toBeVisible();

      await expect
        .poll(async () => readUiReplicas(page), { timeout: 45_000 })
        .toBe(MIN_REPLICAS);

      const baselineStats = await apiStats();
      expect(baselineStats.replicas).toBe(MIN_REPLICAS);
      expect(baselineStats.source).toBe('observe');
      expect(baselineStats.rps).toBeLessThan(TARGET_RPS);

      nodesBefore = await countReadyForgeNodes();
      expect(nodesBefore).toBeGreaterThanOrEqual(MIN_NODES);
      expect(nodesBefore).toBeLessThanOrEqual(MAX_NODES);
      logStep(`baseline ok replicas=1 nodes=${nodesBefore}`);

      // --- Start load ---
      logStep(`starting loadgen rps=${LOAD_RPS} wantPeak=${wantPeak}`);
      await loadgen('start', LOAD_RPS);

      upDesired = await waitPolicyDesiredGe(projectSlug, wantPeak, 120_000);
      expect(upDesired).toBeGreaterThan(MIN_REPLICAS);
      expect(upDesired).toBeGreaterThanOrEqual(wantPeak);
      expect(upDesired).toBeLessThanOrEqual(MAX_REPLICAS);
      logStep(`policy scaled up desired=${upDesired}`);

      // Prefer API/Observe gauge (sync-published) then confirm the dashboard.
      const apiPeak = await waitApiReplicasGe(wantPeak, 120_000);
      expect(apiPeak).toBeLessThanOrEqual(MAX_REPLICAS);
      uiPeak = await waitUiReplicasGe(page, wantPeak, 60_000);
      expect(uiPeak).toBeGreaterThan(MIN_REPLICAS);
      expect(uiPeak).toBeLessThanOrEqual(MAX_REPLICAS);
      logStep(`dashboard peak replicas=${uiPeak}`);

      await expect
        .poll(async () => {
          const t = ((await page.locator('#rps').textContent()) || '').trim();
          return Number(t);
        }, { timeout: 45_000 })
        .toBeGreaterThanOrEqual(TARGET_RPS);

      // --- Optional capacity / node leg (before stop, while demand is high) ---
      if (NODE_LEG) {
        logStep('node leg: waiting for Ready forgenodes scale-up');
        if (nodesBefore < MAX_NODES) {
          nodesAfterUp = await waitReadyNodesGe(nodesBefore + 1, 180_000);
          expect(nodesAfterUp).toBeGreaterThan(nodesBefore);
        } else {
          nodesAfterUp = nodesBefore;
        }
        expect(nodesAfterUp).toBeLessThanOrEqual(MAX_NODES);
        expect(nodesAfterUp).toBeGreaterThanOrEqual(MIN_NODES);
        logStep(`node leg up readyNodes=${nodesAfterUp}`);
      }

      // --- Stop load → scale down (Observe/Grafana checks after idle) ---
      logStep('stopping loadgen');
      await loadgen('stop');
      await setIdleMetrics(IDLE_RPS, MIN_REPLICAS);

      const downDesired = await waitPolicyDesiredEq(
        projectSlug,
        MIN_REPLICAS,
        120_000,
      );
      expect(downDesired).toBe(MIN_REPLICAS);
      expect(downDesired).toBeLessThan(upDesired);
      logStep('policy scaled down to min');

      await waitApiReplicasEq(MIN_REPLICAS, 120_000);
      await waitUiReplicasEq(page, MIN_REPLICAS, 60_000);

      // Dashboard vs Observe consistency at idle. Prometheus scrape can lag;
      // poll briefly, then soft-check via platform.expect if still behind.
      const dashReplicas = await readUiReplicas(page);
      const observeReplicas = await queryObserveReplicas();
      expect(Math.abs(dashReplicas - observeReplicas)).toBeLessThanOrEqual(
        OBSERVE_TOLERANCE,
      );
      let promReplicas = await queryPrometheusReplicas();
      const promTol = Math.max(OBSERVE_TOLERANCE, 1.0);
      if (promReplicas !== null) {
        const promDeadline = Date.now() + 45_000;
        while (
          Date.now() < promDeadline &&
          Math.abs(dashReplicas - promReplicas) > promTol
        ) {
          await page.waitForTimeout(2000);
          promReplicas = await queryPrometheusReplicas();
          if (promReplicas === null) break;
        }
      }
      const grafana = await fetchJson(`${GRAFANA_URL}/api/health`, {}, 5_000);
      expect(grafana.ok).toBe(true);
      logStep(
        `observe consistency dash=${dashReplicas} observe=${observeReplicas} prom=${promReplicas}`,
      );

      // Drain hard-assert only when this run observed a scale-up. If we started
      // already at maxNodes (KEEP=1 residue), defer to the soft infra expect.
      let nodesAfterDown = await countReadyForgeNodes();
      if (NODE_LEG && nodesAfterUp > nodesBefore) {
        logStep('node leg: waiting for drain to minNodes');
        nodesAfterDown = await waitReadyNodesEq(MIN_NODES, 240_000);
        expect(nodesAfterDown).toBe(MIN_NODES);
        expect(nodesAfterDown).toBeLessThan(nodesAfterUp);
      } else if (NODE_LEG) {
        logStep(
          `node leg: skip hard drain wait (no scale-up this run; readyNodes=${nodesAfterDown})`,
        );
      }

      // --- Platform assertions ---
      const scalingResult = await platform.expect(
        'autoscaler',
        async () => {
          const pol = await fetchScalingPolicy(projectSlug);
          if (pol.minReplicas !== MIN_REPLICAS || pol.maxReplicas !== MAX_REPLICAS) {
            throw new Error(
              `bounds mismatch min=${pol.minReplicas} max=${pol.maxReplicas}`,
            );
          }
          if (pol.metricType !== 'httpRequests') {
            throw new Error(
              `lastRecommendation.metricType=${pol.metricType}, want httpRequests`,
            );
          }
          if (
            pol.desiredReplicas < MIN_REPLICAS ||
            pol.desiredReplicas > MAX_REPLICAS
          ) {
            throw new Error(
              `desiredReplicas ${pol.desiredReplicas} outside bounds`,
            );
          }
          if (upDesired < wantPeak || upDesired > MAX_REPLICAS) {
            throw new Error(
              `peak desiredReplicas ${upDesired} not in [${wantPeak},${MAX_REPLICAS}]`,
            );
          }
          if (pol.metricValue !== null && pol.metricValue < 0) {
            throw new Error(`metricValue=${pol.metricValue}`);
          }
        },
        {
          severity: 'major',
          demo: DEMO_ID,
          title:
            'API ScalingPolicy must track httpRequests RPS within [min,max]',
          tested: 'GET ScalingPolicy after loadgen start/stop',
          expected: `metricType=httpRequests; peak desiredReplicas>=${wantPeak}; final=${MIN_REPLICAS}`,
          area: 'forge-autoscaler httpRequests scaling (55.02/55.05)',
          repro: [
            'make demo DEMO=55 KEEP=1',
            'cd tests/e2e && npx playwright test projects/05-pulseboard',
          ],
        },
      );
      expect(scalingResult.outcome).not.toBe('failed');

      const controlResult = await platform.expect(
        'control',
        async () => {
          // Sync + Control reconcile should have actuated Deployment desiredReplicas
          // to match the policy peak we observed (eventually back to min).
          const depNow = await deploymentDesired(apiDeploymentId);
          if (depNow < MIN_REPLICAS || depNow > MAX_REPLICAS) {
            throw new Error(`deployment desiredReplicas=${depNow} outside bounds`);
          }
          if (depNow !== MIN_REPLICAS) {
            // Allow brief lag; soft-fail if still elevated after scale-down wait.
            throw new Error(
              `deployment desiredReplicas=${depNow} after scale-down, want ${MIN_REPLICAS}`,
            );
          }
          const appRes = await fetchJson(
            `${CONTROL_URL}/v1/projects/${projectSlug}/environments/${ENV_NAME}/applications/${API_NAME}`,
          );
          if (!appRes.ok) {
            throw new Error(`GET Application HTTP ${appRes.status}`);
          }
          const app = appRes.body as {
            spec?: { scaling?: { desiredReplicas?: number; minReplicas?: number; maxReplicas?: number } };
          };
          const scaling = app.spec?.scaling || {};
          if (
            Number(scaling.minReplicas) !== MIN_REPLICAS ||
            Number(scaling.maxReplicas) !== MAX_REPLICAS
          ) {
            throw new Error(
              `Application scaling bounds min=${scaling.minReplicas} max=${scaling.maxReplicas}`,
            );
          }
        },
        {
          severity: 'major',
          demo: DEMO_ID,
          title:
            'Control must actuate Application→Deployment replica recommendations',
          tested: `GET Application ${API_NAME} + Deployment ${apiDeploymentId} after scale-down`,
          expected: `desiredReplicas=${MIN_REPLICAS}; bounds [${MIN_REPLICAS},${MAX_REPLICAS}]`,
          area: 'forge-control reconcile + Application scaling (55.02/55.05)',
          repro: [
            'make demo DEMO=55 KEEP=1',
            'cd tests/e2e && npx playwright test projects/05-pulseboard',
          ],
        },
      );
      expect(controlResult.outcome).not.toBe('failed');

      const observeResult = await platform.expect(
        'observe',
        async () => {
          const stats = await apiStats();
          if (stats.source !== 'observe') {
            throw new Error(`/stats source=${stats.source}, want observe`);
          }
          const obs = await queryObserveReplicas();
          if (Math.abs(stats.replicas - obs) > OBSERVE_TOLERANCE) {
            throw new Error(
              `dashboard replicas=${stats.replicas} observe=${obs} tol=${OBSERVE_TOLERANCE}`,
            );
          }
          if (stats.replicas < MIN_REPLICAS || stats.replicas > MAX_REPLICAS) {
            throw new Error(`/stats replicas=${stats.replicas} outside bounds`);
          }
          const g = await fetchJson(`${GRAFANA_URL}/api/health`, {}, 5_000);
          if (!g.ok) throw new Error(`Grafana health HTTP ${g.status}`);
          const prom = await queryPrometheusReplicas();
          if (
            prom !== null &&
            Math.abs(stats.replicas - prom) > Math.max(OBSERVE_TOLERANCE, 1.0)
          ) {
            throw new Error(
              `Prometheus scrape lag replicas=${prom} vs /stats=${stats.replicas}`,
            );
          }
        },
        {
          severity: 'major',
          demo: DEMO_ID,
          title:
            'Dashboard /stats must match Observe/Grafana replica series',
          tested: 'GET /stats + PromQL sum(forge_replicas_ready) + Grafana /api/health',
          expected: `source=observe; |dash-observe|<=${OBSERVE_TOLERANCE}; Grafana healthy; Prometheus within lag tol`,
          area: 'forge-observe / demo55-metrics surfacing (55.04/55.05)',
          repro: [
            'make demo DEMO=55 KEEP=1',
            'curl -fsS -H Host:api.pulseboard.localhost http://127.0.0.1:4000/stats',
            `curl -fsS --get ${METRICS_URL}/api/v1/query --data-urlencode 'query=sum(forge_replicas_ready{application="pulseboard-api"})'`,
          ],
        },
      );
      expect(observeResult.outcome).not.toBe('failed');

      if (NODE_LEG) {
        const infraResult = await platform.expect(
          'infrastructure',
          async () => {
            const n = await countReadyForgeNodes();
            if (n < MIN_NODES || n > MAX_NODES) {
              throw new Error(
                `readyNodes=${n} outside [${MIN_NODES},${MAX_NODES}]`,
              );
            }
            if (nodesBefore < MAX_NODES && nodesAfterUp <= nodesBefore) {
              throw new Error(
                `node scale-up did not increase (before=${nodesBefore} after=${nodesAfterUp})`,
              );
            }
            // Drain to min is required when this run grew the pool; otherwise
            // bounds-only (stale maxNodes from a prior KEEP=1 deploy).
            if (nodesAfterUp > nodesBefore && n !== MIN_NODES) {
              throw new Error(
                `after drain readyNodes=${n}, want ${MIN_NODES}`,
              );
            }
            const pool = await fetchJson(
              `${CONTROL_URL}/v1/nodepools/${POOL_NAME}`,
            );
            if (!pool.ok) {
              throw new Error(`GET NodePool HTTP ${pool.status}`);
            }
          },
          {
            severity: 'major',
            demo: DEMO_ID,
            title:
              'NodePool must scale up on unschedulability and drain to minNodes',
            tested: `ready forgenodes under load then after idle (pool=${POOL_NAME})`,
            expected: `peak > baseline when below max; final readyNodes in [${MIN_NODES},${MAX_NODES}]`,
            area: 'forge-infrastructure Docker NodePool (55.03/55.05)',
            repro: [
              'make demo DEMO=55 KEEP=1',
              'PULSEBOARD_E2E_NODE_LEG=1 cd tests/e2e && npx playwright test projects/05-pulseboard',
            ],
          },
        );
        expect(infraResult.outcome).not.toBe('failed');
      }
    } finally {
      try {
        await loadgen('stop');
      } catch {
        /* best-effort */
      }
      stopChild(sync);
    }
  });
});
