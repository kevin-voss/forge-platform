import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

import { platform } from '../../harness/findings';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://shop.orderpipe.localhost:4000';
const API_URL =
  process.env.FORGE_E2E_API_URL ?? 'http://api.orderpipe.localhost:4000';
const FULFILLMENT_URL =
  process.env.FORGE_E2E_FULFILLMENT_URL ??
  'http://fulfillment.orderpipe.localhost:4000';
const DISCOVERY_URL =
  process.env.FORGE_DISCOVERY_HOST_URL ?? 'http://127.0.0.1:4109';
const NETWORK_URL = process.env.FORGE_NETWORK_URL ?? 'http://127.0.0.1:4110';
const WORKFLOWS_URL =
  process.env.FORGE_WORKFLOWS_URL ?? 'http://127.0.0.1:4302';
const DISC_PROJECT = process.env.FORGE_DISCOVERY_DEFAULT_PROJECT ?? 'orderpipe';
const DISC_ENV = process.env.FORGE_DISCOVERY_DEFAULT_ENVIRONMENT ?? 'local';
const DEMO_ID = '04-orderpipe';

const repoRoot = path.resolve(__dirname, '../../../..');
const demoDir = path.join(repoRoot, 'demos/54-orderpipe');
const statePath = path.join(demoDir, '.demo-state');
const checkDiscovery = path.join(demoDir, 'check-discovery.sh');
const renewDiscovery = path.join(demoDir, 'scripts/renew-discovery.sh');

type SagaEvent = { step?: string; outcome?: string };
type Order = {
  id: string;
  status: string;
  customerEmail?: string;
  declineCharge?: boolean;
  sagaEvents?: SagaEvent[];
};

function readDemoState(): Record<string, string> {
  if (!fs.existsSync(statePath)) {
    throw new Error(
      `missing ${statePath}; deploy OrderPipe (demos/54-orderpipe/run.sh) first`,
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

async function apiJson<T>(
  base: string,
  pathName: string,
  init: RequestInit = {},
): Promise<{ status: number; body: T; text: string }> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('content-type') && !(init.body instanceof FormData)) {
    headers.set('content-type', 'application/json');
  }
  const res = await fetch(`${base}${pathName}`, { ...init, headers });
  const text = await res.text();
  let body = undefined as unknown as T;
  if (text) {
    try {
      body = JSON.parse(text) as T;
    } catch {
      body = text as unknown as T;
    }
  }
  return { status: res.status, body, text };
}

async function getOrder(id: string): Promise<Order> {
  const { status, body, text } = await apiJson<Order>(API_URL, `/orders/${id}`);
  if (status !== 200) {
    throw new Error(`GET /orders/${id} HTTP ${status}: ${text.slice(0, 200)}`);
  }
  return body;
}

async function waitOrderStatus(
  id: string,
  want: string | string[],
  timeoutMs = 120_000,
): Promise<Order> {
  const wanted = Array.isArray(want) ? want : [want];
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    const order = await getOrder(id);
    last = order.status;
    if (wanted.includes(order.status)) return order;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(
    `order ${id} never reached ${wanted.join('|')} (last=${last})`,
  );
}

function countSaga(events: SagaEvent[] | undefined, step: string, outcome: string): number {
  return (events || []).filter((e) => e.step === step && e.outcome === outcome).length;
}

async function placeOrderViaUI(
  page: Page,
  email: string,
  sku = 'mug',
  qty = '1',
): Promise<string> {
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('SKU').selectOption(sku);
  await page.getByLabel('Qty').fill(qty);
  await page.getByRole('button', { name: /place order/i }).click();
  await expect(page.getByText(/Order placed/i)).toBeVisible({ timeout: 30_000 });
  const summary = page.locator('#order-summary');
  await expect(summary).toContainText(email, { timeout: 15_000 });
  const text = (await summary.textContent()) || '';
  const id = text.trim().split(/\s+—\s+/)[0]?.trim();
  if (!id || id.length < 8) {
    throw new Error(`could not parse order id from summary: ${text}`);
  }
  return id;
}

async function readDeniedCounter(): Promise<number> {
  const res = await fetch(`${NETWORK_URL}/metrics`);
  if (!res.ok) throw new Error(`GET network /metrics HTTP ${res.status}`);
  const text = await res.text();
  const m = text.match(/^forge_network_policy_denied_total\s+(\d+(?:\.\d+)?)/m);
  return m ? Number(m[1]) : 0;
}

/**
 * OrderPipe browser E2E (54.06): happy order → notified; declined charge →
 * retry + compensation → refunded; NetworkPolicy denied pair blocked.
 * Platform soft-asserts: discovery, network deny metric, workflow saga DB state.
 */
test.describe('04-orderpipe', () => {
  test.describe.configure({ mode: 'serial' });

  test('happy → notified; declined → refunded; network policy blocked', async ({
    page,
  }) => {
    test.setTimeout(300_000);

    await page.goto(BASE_URL);
    await expect(page.getByText('OrderPipe', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /^shop$/i })).toBeVisible();
    await expect(
      page.locator('#catalog-list .catalog-item').filter({ hasText: /Forge Mug/i }),
    ).toBeVisible({ timeout: 15_000 });

    // --- Product: happy path place → statuses advance to notified ---
    const happyEmail = `e2e-happy-${Date.now()}@example.com`;
    const happyId = await placeOrderViaUI(page, happyEmail);
    await expect(page.locator('#order-summary')).toContainText(/placed|validated|charged|fulfilled|notified/i);

    const happy = await waitOrderStatus(happyId, 'notified', 180_000);
    expect(happy.customerEmail).toBe(happyEmail);
    expect(happy.declineCharge || false).toBe(false);
    expect(countSaga(happy.sagaEvents, 'validate', 'ok')).toBeGreaterThanOrEqual(1);
    expect(countSaga(happy.sagaEvents, 'charge', 'ok')).toBeGreaterThanOrEqual(1);
    expect(countSaga(happy.sagaEvents, 'fulfill', 'ok')).toBeGreaterThanOrEqual(1);
    expect(countSaga(happy.sagaEvents, 'notify', 'ok')).toBeGreaterThanOrEqual(1);
    expect(countSaga(happy.sagaEvents, 'charge', 'compensated')).toBe(0);

    // Reload UI and confirm last-order summary still reflects a placed order id.
    await page.reload();
    await expect(page.getByText('OrderPipe', { exact: true })).toBeVisible();

    // --- Product: declined charge → retries → refunded (no fulfill/notify) ---
    // Injectable decline: email containing "+declined@" (or declineCharge:true).
    const declineEmail = `e2e-${Date.now()}+declined@example.com`;
    const declineId = await placeOrderViaUI(page, declineEmail);
    const declined = await waitOrderStatus(declineId, ['refunded', 'failed'], 120_000);
    expect(declined.status).toBe('refunded');
    expect(declined.declineCharge).toBe(true);
    const retries = countSaga(declined.sagaEvents, 'charge', 'retry');
    expect(retries).toBeGreaterThanOrEqual(1);
    expect(countSaga(declined.sagaEvents, 'charge', 'compensated')).toBe(1);
    expect(countSaga(declined.sagaEvents, 'fulfill', 'ok')).toBe(0);
    expect(countSaga(declined.sagaEvents, 'notify', 'ok')).toBe(0);

    // --- Product: NetworkPolicy proof via fulfillment debug endpoint ---
    const state = readDemoState();
    const fromWl = state.FULFILLMENT_DEPLOYMENT_ID;
    const toWl = state.NOTIFY_DEPLOYMENT_ID;
    expect(fromWl).toBeTruthy();
    expect(toWl).toBeTruthy();

    const allow = await apiJson<{ orderId?: string }>(FULFILLMENT_URL, '/fulfill', {
      method: 'POST',
      body: JSON.stringify({ orderId: `policy-allow-${Date.now()}` }),
    });
    expect(allow.status).toBe(202);

    const denyBefore = await readDeniedCounter();
    const denied = await apiJson<{
      blocked?: boolean;
      event?: string;
      pair?: string;
      notifyAttempted?: boolean;
    }>(FULFILLMENT_URL, '/debug/denied-call', {
      method: 'POST',
      body: JSON.stringify({
        fromWorkload: fromWl,
        toWorkload: toWl,
        reason: 'networkpolicy:policy-default-deny',
      }),
    });
    expect(denied.status).toBe(403);
    expect(denied.body.blocked).toBe(true);
    expect(denied.body.event).toBe('network.policy.denied');
    expect(denied.body.pair).toBe('fulfillment→notify');
    expect(denied.body.notifyAttempted).toBe(false);

    // --- Platform assertions ---
    // Product contract: no hard-coded peer DNS in order-api source.
    execFileSync('bash', [checkDiscovery], { cwd: demoDir, stdio: 'pipe' });

    const discoveryResult = await platform.expect(
      'discovery',
      async () => {
        // Short demo leases expire; renew Ready endpoints before asserting.
        execFileSync('bash', [renewDiscovery], { cwd: demoDir, stdio: 'pipe' });
        for (const service of ['api', 'fulfillment', 'notify']) {
          const res = await fetch(
            `${DISCOVERY_URL}/v1/projects/${DISC_PROJECT}/environments/${DISC_ENV}/services/${service}/endpoints`,
          );
          if (!res.ok) {
            throw new Error(`list ${service} endpoints HTTP ${res.status}`);
          }
          const eps = (await res.json()) as Array<{
            phase?: string;
            ready?: boolean;
            address?: { ip?: string; port?: number };
          }>;
          const ready = (eps || []).filter(
            (e) => e.phase === 'Ready' && e.ready === true && !!e.address?.ip,
          );
          if (!ready.length) {
            throw new Error(
              `no Ready discovery endpoints for ${service} (got ${JSON.stringify(eps).slice(0, 300)})`,
            );
          }
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Discovery must resolve OrderPipe peers (*.svc.forge; no hard-coded DNS)',
        tested: `${renewDiscovery} + list Ready endpoints for api/fulfillment/notify`,
        expected: '≥1 Ready endpoint per peer service (api/fulfillment/notify)',
        area: 'forge-discovery peer resolution (54.02/54.06)',
        repro: [
          'make demo DEMO=54 KEEP=1',
          'bash demos/54-orderpipe/scripts/renew-discovery.sh',
          'cd tests/e2e && npx playwright test projects/04-orderpipe',
        ],
      },
    );
    expect(discoveryResult.outcome).not.toBe('failed');

    const networkResult = await platform.expect(
      'network',
      async () => {
        const after = await readDeniedCounter();
        if (!(after > denyBefore)) {
          throw new Error(
            `forge_network_policy_denied_total did not increase (before=${denyBefore} after=${after})`,
          );
        }
        if (denied.status !== 403 || denied.body.blocked !== true) {
          throw new Error(`denied-call not blocked: HTTP ${denied.status} ${denied.text}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'NetworkPolicy must block fulfillment→notify and bump deny metric',
        tested: 'POST fulfillment /debug/denied-call + forge_network_policy_denied_total',
        expected: 'HTTP 403 blocked=true; deny counter increases; allow /fulfill=202',
        area: 'forge-network policy enforcement (54.03/54.06)',
        repro: [
          'make demo DEMO=54 KEEP=1',
          'cd tests/e2e && npx playwright test projects/04-orderpipe',
        ],
      },
    );
    expect(networkResult.outcome).not.toBe('failed');

    const workflowResult = await platform.expect(
      'workflows',
      async () => {
        const list = await fetch(`${WORKFLOWS_URL}/v1/workflows`, {
          headers: { 'X-Forge-Project': 'orderpipe' },
        });
        if (!list.ok) throw new Error(`GET workflows HTTP ${list.status}`);
        const raw = (await list.json()) as
          | Array<string | { name?: string; id?: string }>
          | { workflows?: Array<string | { name?: string; id?: string }>; items?: Array<string | { name?: string; id?: string }> };
        const items = Array.isArray(raw)
          ? raw
          : raw.workflows || raw.items || [];
        const names = items.map((it) =>
          typeof it === 'string' ? it : it.name || it.id || '',
        );
        if (!names.includes('order-saga')) {
          throw new Error(`order-saga missing from workflows: ${JSON.stringify(raw).slice(0, 400)}`);
        }

        // Final DB state matches saga outcome (happy notified; declined compensated).
        const happyNow = await getOrder(happyId);
        if (happyNow.status !== 'notified') {
          throw new Error(`happy order status=${happyNow.status}, want notified`);
        }
        if (countSaga(happyNow.sagaEvents, 'charge', 'ok') < 1) {
          throw new Error('happy order missing charge ok saga event');
        }

        const declinedNow = await getOrder(declineId);
        if (declinedNow.status !== 'refunded') {
          throw new Error(`declined order status=${declinedNow.status}, want refunded`);
        }
        if (countSaga(declinedNow.sagaEvents, 'charge', 'retry') < 1) {
          throw new Error('declined order missing charge retry events');
        }
        if (countSaga(declinedNow.sagaEvents, 'charge', 'compensated') !== 1) {
          throw new Error('declined order missing exactly one charge compensated');
        }
        if (
          countSaga(declinedNow.sagaEvents, 'fulfill', 'ok') > 0 ||
          countSaga(declinedNow.sagaEvents, 'notify', 'ok') > 0
        ) {
          throw new Error('declined order must not fulfill/notify after compensation');
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Workflow saga retries + compensation must leave consistent DB state',
        tested: 'order-saga listed; happy→notified; declined→retry+refunded; no half-fulfill',
        expected:
          'order-saga present; notified with charge ok; refunded with retries + compensated once',
        area: 'forge-workflows / OrderPipe saga (54.05/54.06; F-008 engine HTTP gap)',
        repro: [
          'make demo DEMO=54 KEEP=1',
          'cd tests/e2e && npx playwright test projects/04-orderpipe',
        ],
      },
    );
    expect(workflowResult.outcome).not.toBe('failed');

    // Events consistency: final statuses already match choreography outcomes above.
    const eventsResult = await platform.expect(
      'events',
      async () => {
        const happyNow = await getOrder(happyId);
        const declinedNow = await getOrder(declineId);
        for (const step of ['validate', 'charge', 'fulfill', 'notify'] as const) {
          if (countSaga(happyNow.sagaEvents, step, 'ok') < 1) {
            throw new Error(`happy order missing ${step} ok (event choreography / saga audit)`);
          }
        }
        if (declinedNow.status !== 'refunded') {
          throw new Error(`declined terminal=${declinedNow.status}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Order events / saga audit must match final DB state',
        tested: `GET /orders/${happyId} + /orders/${declineId} sagaEvents`,
        expected: 'happy has validate→notify ok; declined terminal refunded',
        area: 'forge-events choreography + saga audit (54.04/54.06)',
        repro: [
          'make demo DEMO=54 KEEP=1',
          'cd tests/e2e && npx playwright test projects/04-orderpipe',
        ],
      },
    );
    expect(eventsResult.outcome).not.toBe('failed');
  });
});
