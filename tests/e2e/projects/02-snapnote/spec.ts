import { expect, test, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';

import { platform } from '../../harness/findings';

const execFileAsync = promisify(execFile);

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://app.snapnote.localhost:4000';
const API_URL =
  process.env.FORGE_E2E_API_URL ?? 'http://api.snapnote.localhost:4000';
const AUTOSCALER_URL =
  process.env.FORGE_AUTOSCALER_URL ?? 'http://127.0.0.1:4112';
const CONTROL_URL = process.env.FORGE_CONTROL_URL ?? 'http://127.0.0.1:4001';
const STORAGE_URL =
  process.env.FORGE_STORAGE_HOST_URL ?? 'http://127.0.0.1:4107';
const METRICS_URL =
  process.env.FORGE_DEMO52_METRICS_URL ?? 'http://127.0.0.1:4198';
const STORAGE_BUCKET = process.env.FORGE_STORAGE_BUCKET ?? 'snapnote-attachments';
const STORAGE_PROJECT = process.env.FORGE_STORAGE_PROJECT ?? 'snapnote';
const ENV_NAME = process.env.FORGE_ENVIRONMENT ?? 'local';
const WORKER_POLICY = 'snapnote-worker-queue';
const WORKER_NAME = 'snapnote-worker';
const QUEUE_NAME = 'snapnote-attachments';
const MIN_REPLICAS = 1;
const MAX_REPLICAS = 8;
const TARGET_PER_REPLICA = 20;
const BURST_COUNT = Number(process.env.SNAPNOTE_E2E_BURST_COUNT ?? 40);
const BURST_DEPTH = Number(process.env.SNAPNOTE_E2E_BURST_DEPTH ?? 80);
const DEMO_ID = '02-snapnote';

const repoRoot = path.resolve(__dirname, '../../../..');
const demoDir = path.join(repoRoot, 'demos/52-snapnote');
const statePath = path.join(demoDir, '.demo-state');
const burstScript = path.join(demoDir, 'scripts/burst.sh');

/** Tiny valid JPEG for setInputFiles. */
const TINY_JPEG = Buffer.from(
  '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAn/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/8QAFQEBAQAAAAAAAAAAAAAAAAAAAAX/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIRAxEAPwCwAA8A/9k=',
  'base64',
);

function readDemoState(): Record<string, string> {
  if (!fs.existsSync(statePath)) {
    throw new Error(`missing ${statePath}; deploy SnapNote (demos/52-snapnote/run.sh) first`);
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

function peakMinReplicas(depth: number): number {
  return Math.min(MAX_REPLICAS, Math.max(MIN_REPLICAS, Math.ceil(depth / TARGET_PER_REPLICA)));
}

async function publishQueueMetrics(depth: number, retryRate = 0): Promise<void> {
  const res = await fetch(`${METRICS_URL}/demo/queue/${QUEUE_NAME}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      depth,
      oldestAgeSeconds: 15,
      consumerLag: depth,
      retryRate,
    }),
  });
  if (!res.ok) {
    throw new Error(`publish queue metrics HTTP ${res.status}`);
  }
}

async function clearQueueMetrics(): Promise<void> {
  await publishQueueMetrics(0, 0);
}

async function fetchScalingPolicy(projectSlug: string): Promise<{
  desiredReplicas: number;
  minReplicas: number;
  maxReplicas: number;
  metricType: string;
  metricValue: number | null;
  conditions: Array<{ reason?: string }>;
}> {
  const res = await fetch(
    `${AUTOSCALER_URL}/v1/projects/${projectSlug}/environments/${ENV_NAME}/scalingpolicies/${WORKER_POLICY}`,
  );
  if (!res.ok) {
    throw new Error(`GET ScalingPolicy HTTP ${res.status}`);
  }
  const body = (await res.json()) as {
    spec?: { minReplicas?: number; maxReplicas?: number };
    status?: {
      desiredReplicas?: number;
      lastRecommendation?: { metricType?: string; metricValue?: number | null };
      conditions?: Array<{ reason?: string }>;
    };
  };
  return {
    desiredReplicas: Number(body.status?.desiredReplicas ?? 0),
    minReplicas: Number(body.spec?.minReplicas ?? MIN_REPLICAS),
    maxReplicas: Number(body.spec?.maxReplicas ?? MAX_REPLICAS),
    metricType: String(body.status?.lastRecommendation?.metricType ?? ''),
    metricValue:
      body.status?.lastRecommendation?.metricValue === undefined
        ? null
        : Number(body.status.lastRecommendation.metricValue),
    conditions: body.status?.conditions ?? [],
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
    await publishQueueMetrics(BURST_DEPTH, 0).catch(() => undefined);
    const pol = await fetchScalingPolicy(projectSlug);
    last = pol.desiredReplicas;
    if (last >= min) return last;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`ScalingPolicy desiredReplicas never reached ${min} (last=${last})`);
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
  throw new Error(`ScalingPolicy desiredReplicas never == ${want} (last=${last})`);
}

async function listAttachments(
  noteId: string,
): Promise<Array<{ id: string; status: string; thumbnailKey?: string; objectKey: string }>> {
  const res = await fetch(`${API_URL}/notes/${noteId}/attachments`);
  if (!res.ok) {
    throw new Error(`GET attachments HTTP ${res.status}`);
  }
  return (await res.json()) as Array<{
    id: string;
    status: string;
    thumbnailKey?: string;
    objectKey: string;
  }>;
}

async function waitAttachmentsReady(noteId: string, timeoutMs = 240_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let pending = -1;
  while (Date.now() < deadline) {
    const items = await listAttachments(noteId);
    pending = items.filter((a) => a.status !== 'ready').length;
    if (pending === 0 && items.length > 0) return;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`attachments on note ${noteId} never all ready (pending=${pending})`);
}

async function workerContainerId(deploymentId: string): Promise<string> {
  const { stdout: byLabel } = await execFileAsync(
    'docker',
    [
      'ps',
      '-q',
      '--filter',
      `label=forge.deployment_id=${deploymentId}`,
      '--filter',
      'label=forge.managed=true',
    ],
    { encoding: 'utf8' },
  );
  const labeled = byLabel.trim().split('\n').filter(Boolean)[0];
  if (labeled) return labeled;

  const short = deploymentId.replace(/-/g, '').slice(0, 8);
  const { stdout: byName } = await execFileAsync(
    'docker',
    [
      'ps',
      '-q',
      '--filter',
      'label=forge.managed=true',
      '--filter',
      `name=forge-worker-${short}-`,
    ],
    { encoding: 'utf8' },
  );
  const named = byName.trim().split('\n').filter(Boolean)[0];
  if (!named) {
    throw new Error(`no running worker container for deployment ${deploymentId}`);
  }
  return named;
}

async function waitWorkerReady(timeoutMs = 180_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    try {
      const res = await fetch('http://worker.snapnote.localhost:4000/health/ready');
      last = `HTTP ${res.status}`;
      if (res.ok) return;
    } catch (err) {
      last = err instanceof Error ? err.message : String(err);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`worker not ready after restart (${last})`);
}

async function readWorkersIndicator(page: Page): Promise<number> {
  const el = page.locator('#workers-indicator');
  await expect(el).toBeVisible();
  const raw = (await el.getAttribute('data-replicas')) || '0';
  return Number(raw);
}

async function waitWorkersIndicatorGe(page: Page, min: number, timeoutMs = 120_000): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let last = 0;
  while (Date.now() < deadline) {
    last = await readWorkersIndicator(page);
    if (last >= min) return last;
    await page.waitForTimeout(1000);
  }
  throw new Error(`workers indicator never reached ${min} (last=${last})`);
}

async function createTempJpeg(name: string): Promise<string> {
  const filePath = path.join(os.tmpdir(), name);
  fs.writeFileSync(filePath, TINY_JPEG);
  return filePath;
}

/**
 * SnapNote browser E2E (52.05): create note → attach → async thumbnail,
 * burst → workers scale up → drain → scale down; platform.expect for
 * exactly-once restart, ScalingPolicy bounds, thumbnail retrieval.
 */
test.describe('02-snapnote', () => {
  test.describe.configure({ mode: 'serial' });

  test('upload → async thumbnail → burst → workers scale → drain', async ({ page }) => {
    test.setTimeout(600_000);

    const state = readDemoState();
    const projectSlug = state.PROJECT_SLUG;
    if (!projectSlug) throw new Error('PROJECT_SLUG missing from .demo-state');
    const workerDeploymentId = state.WORKER_DEPLOYMENT_ID;
    if (!workerDeploymentId) throw new Error('WORKER_DEPLOYMENT_ID missing from .demo-state');

    // Unique per run so leftover seed "Trip photos" / prior KEEP=1 bursts don't collide.
    const noteTitle = `Trip photos ${Date.now().toString(36)}`;
    const imagePath = await createTempJpeg(`snapnote-e2e-${Date.now()}.jpg`);

    // --- Product path: create note + single upload → processing → thumbnail ---
    await page.goto(BASE_URL);
    await expect(page.getByText('SnapNote', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /notes with attachments/i })).toBeVisible();
    await expect(page.locator('#workers-indicator')).toBeVisible();

    await page.getByLabel('Title').fill(noteTitle);
    await page.getByLabel('Body').fill('E2E async thumbnail proof');
    await page.getByRole('button', { name: /add note/i }).click();
    const noteRow = page
      .locator('#note-list > li')
      .filter({ has: page.locator('.title', { hasText: noteTitle }) });
    await expect(noteRow).toBeVisible({ timeout: 15_000 });

    const baselineWorkers = await readWorkersIndicator(page);

    // Set files directly — avoid clicking Attach (native chooser races headed mode).
    await noteRow.locator('input[type="file"]').setInputFiles(imagePath);

    // SPA holds an optimistic "processing…" row ~600ms; under load the worker may
    // still win the race, so accept processing → ready via row or status line.
    const processing = noteRow.getByText(/processing…/i);
    const thumb = noteRow.locator('.thumb-key').first();
    const statusLine = page.locator('#note-status');
    const first = await Promise.race([
      processing.waitFor({ state: 'visible', timeout: 45_000 }).then(() => 'processing' as const),
      thumb.waitFor({ state: 'visible', timeout: 45_000 }).then(() => 'ready' as const),
      statusLine
        .filter({ hasText: /processing thumbnail|Thumbnail ready|Uploading/i })
        .waitFor({ state: 'visible', timeout: 45_000 })
        .then(() => 'status' as const),
    ]);
    expect(['processing', 'ready', 'status']).toContain(first);

    await expect(thumb).toBeVisible({ timeout: 120_000 });
    await expect(noteRow.getByText(/· ready/i).first()).toBeVisible();
    await expect(statusLine).toContainText(/Thumbnail ready/i, { timeout: 120_000 });

    // Resolve note id for burst / platform checks.
    const notesRes = await fetch(`${API_URL}/notes`);
    expect(notesRes.status).toBe(200);
    const notes = (await notesRes.json()) as Array<{ id: string; title: string }>;
    const trip = notes.find((n) => n.title === noteTitle);
    expect(trip).toBeTruthy();
    const noteId = trip!.id;

    const singleAtts = await listAttachments(noteId);
    const single = singleAtts.find((a) => a.status === 'ready' && a.thumbnailKey);
    expect(single).toBeTruthy();
    const singleThumbKey = single!.thumbnailKey!;

    // --- Burst: enqueue backlog + publish queueDepth so autoscaler scales up ---
    const wantPeak = peakMinReplicas(BURST_DEPTH);
    await publishQueueMetrics(BURST_DEPTH, 0);

    const burstOut = path.join(os.tmpdir(), `snapnote-burst-${Date.now()}.out`);
    const burstResult = await execFileAsync(
      'bash',
      [burstScript, '--count', String(BURST_COUNT), '--depth', String(BURST_DEPTH), '--note-id', noteId],
      {
        encoding: 'utf8',
        env: {
          ...process.env,
          GATEWAY_URL: process.env.FORGE_GATEWAY_URL ?? 'http://127.0.0.1:4000',
          API_HOST: 'api.snapnote.localhost',
          STORAGE_URL,
          METRICS_URL,
          QUEUE_NAME,
          PUBLISH_METRICS: '1',
        },
      },
    );
    fs.writeFileSync(burstOut, `${burstResult.stdout}\n${burstResult.stderr}`);

    // Keep depth high while the autoscaler evaluates (real queue may drain fast).
    await publishQueueMetrics(BURST_DEPTH, 0);

    // Restart worker mid-burst to prove redelivery + idempotency.
    const workerCid = await workerContainerId(workerDeploymentId);
    await execFileAsync('docker', ['restart', workerCid]);
    await waitWorkerReady();

    const upDesired = await waitPolicyDesiredGe(projectSlug, wantPeak, 120_000);
    expect(upDesired).toBeGreaterThanOrEqual(wantPeak);
    expect(upDesired).toBeLessThanOrEqual(MAX_REPLICAS);
    expect(upDesired).toBeGreaterThanOrEqual(baselineWorkers);

    const uiPeak = await waitWorkersIndicatorGe(page, wantPeak, 120_000);
    expect(uiPeak).toBeGreaterThanOrEqual(wantPeak);
    expect(uiPeak).toBeLessThanOrEqual(MAX_REPLICAS);

    // Drain backlog (real attachments) while holding metrics high briefly, then clear.
    await waitAttachmentsReady(noteId, 240_000);
    await clearQueueMetrics();
    const downDesired = await waitPolicyDesiredEq(projectSlug, MIN_REPLICAS, 120_000);
    expect(downDesired).toBe(MIN_REPLICAS);

    await expect
      .poll(async () => readWorkersIndicator(page), { timeout: 120_000 })
      .toBe(MIN_REPLICAS);

    const finalAtts = await listAttachments(noteId);
    expect(finalAtts.length).toBeGreaterThanOrEqual(1 + BURST_COUNT);
    expect(finalAtts.every((a) => a.status === 'ready')).toBe(true);
    const thumbKeys = finalAtts.map((a) => a.thumbnailKey || '');
    expect(thumbKeys.every((k) => k.length > 0)).toBe(true);
    // Exactly-once: each attachment has its own thumbnail key; no blank duplicates.
    expect(new Set(thumbKeys).size).toBe(thumbKeys.length);

    // --- Platform assertions ---
    const scalingResult = await platform.expect(
      'autoscaler',
      async () => {
        const pol = await fetchScalingPolicy(projectSlug);
        if (pol.minReplicas !== MIN_REPLICAS || pol.maxReplicas !== MAX_REPLICAS) {
          throw new Error(
            `bounds mismatch min=${pol.minReplicas} max=${pol.maxReplicas} want [${MIN_REPLICAS},${MAX_REPLICAS}]`,
          );
        }
        if (pol.metricType !== 'queueDepth') {
          throw new Error(`lastRecommendation.metricType=${pol.metricType}, want queueDepth`);
        }
        if (pol.desiredReplicas < MIN_REPLICAS || pol.desiredReplicas > MAX_REPLICAS) {
          throw new Error(`desiredReplicas ${pol.desiredReplicas} outside bounds`);
        }
        // Peak we observed during the run must have stayed in bounds.
        if (upDesired < MIN_REPLICAS || upDesired > MAX_REPLICAS) {
          throw new Error(`peak desiredReplicas ${upDesired} outside bounds`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Worker ScalingPolicy must track queueDepth within [min,max]',
        tested: 'GET ScalingPolicy status after burst + drain',
        expected: `metricType=queueDepth; desiredReplicas in [${MIN_REPLICAS},${MAX_REPLICAS}]; peak>=${wantPeak}`,
        area: 'forge-autoscaler queueDepth worker scaling (52.04/52.05)',
        repro: [
          'make demo DEMO=52 KEEP=1',
          'cd tests/e2e && npx playwright test projects/02-snapnote',
        ],
      },
    );
    expect(scalingResult.outcome).not.toBe('failed');

    const idempotencyResult = await platform.expect(
      'events',
      async () => {
        // Re-check after worker restart: every attachment ready once, unique thumbs.
        const items = await listAttachments(noteId);
        if (items.length < 1 + BURST_COUNT) {
          throw new Error(`expected >= ${1 + BURST_COUNT} attachments, got ${items.length}`);
        }
        for (const att of items) {
          if (att.status !== 'ready') {
            throw new Error(`attachment ${att.id} status=${att.status}`);
          }
          if (!att.thumbnailKey) {
            throw new Error(`attachment ${att.id} missing thumbnailKey`);
          }
        }
        const keys = items.map((a) => a.thumbnailKey!);
        if (new Set(keys).size !== keys.length) {
          throw new Error('duplicate thumbnailKey values — exactly-once violated');
        }

        // Worker resource still present with scaling bounds.
        const wRes = await fetch(
          `${CONTROL_URL}/v1/projects/${projectSlug}/environments/${ENV_NAME}/workers/${WORKER_NAME}`,
        );
        if (!wRes.ok) throw new Error(`GET Worker HTTP ${wRes.status}`);
        const worker = (await wRes.json()) as {
          spec?: { scaling?: { minReplicas?: number; maxReplicas?: number } };
        };
        const min = worker.spec?.scaling?.minReplicas;
        const max = worker.spec?.scaling?.maxReplicas;
        if (min !== MIN_REPLICAS || max !== MAX_REPLICAS) {
          throw new Error(`Worker scaling bounds min=${min} max=${max}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'attachment.uploaded must process exactly once across worker restart',
        tested: 'docker restart worker mid-burst; all attachments ready with unique thumbnailKey',
        expected: 'no lost attachments; no duplicate thumbnails after redelivery',
        area: 'forge-events durable queue + worker idempotency (52.03/52.05)',
        repro: [
          'make demo DEMO=52 KEEP=1',
          'demos/52-snapnote/scripts/burst.sh --count 40',
          'docker restart $(docker ps -q --filter name=forge-worker- | head -1)',
        ],
      },
    );
    expect(idempotencyResult.outcome).not.toBe('failed');

    const storageResult = await platform.expect(
      'storage',
      async () => {
        const encoded = singleThumbKey
          .split('/')
          .map((part) => encodeURIComponent(part))
          .join('/');
        const res = await fetch(
          `${STORAGE_URL}/v1/buckets/${STORAGE_BUCKET}/objects/${encoded}`,
          { headers: { 'X-Forge-Project': STORAGE_PROJECT } },
        );
        if (!res.ok) {
          throw new Error(`thumbnail GET HTTP ${res.status} key=${singleThumbKey}`);
        }
        const buf = Buffer.from(await res.arrayBuffer());
        if (!buf.subarray(0, 6).equals(Buffer.from('THUMB\n'))) {
          throw new Error(`thumbnail missing THUMB marker: ${buf.subarray(0, 40).toString()}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Thumbnail object must be retrievable from Forge Storage',
        tested: `GET /v1/buckets/${STORAGE_BUCKET}/objects/{thumbnailKey}`,
        expected: 'HTTP 200 body starting with THUMB\\n',
        area: 'forge-storage object round-trip (52.02/52.05)',
        repro: [
          'make demo DEMO=52 KEEP=1',
          'cd tests/e2e && npx playwright test projects/02-snapnote',
        ],
      },
    );
    expect(storageResult.outcome).not.toBe('failed');
  });
});
