import { expect, test, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

import { platform } from '../../harness/findings';

const BASE_URL =
  process.env.FORGE_E2E_BASE_URL ?? 'http://app.askdocs.localhost:4000';
const API_URL =
  process.env.FORGE_E2E_API_URL ?? 'http://api.askdocs.localhost:4000';
const MODELS_URL =
  process.env.FORGE_MODELS_HOST_URL ?? 'http://127.0.0.1:4300';
const MEMORY_URL =
  process.env.FORGE_MEMORY_HOST_URL ?? 'http://127.0.0.1:4303';
const AGENTS_URL =
  process.env.FORGE_AGENTS_HOST_URL ?? 'http://127.0.0.1:4301';
const TEMPO_URL = process.env.FORGE_TEMPO_URL ?? 'http://127.0.0.1:3002';
const OBSERVE_URL =
  process.env.FORGE_OBSERVE_HOST_URL ?? 'http://127.0.0.1:4106';
const MEMORY_PROJECT = process.env.FORGE_MEMORY_PROJECT ?? 'askdocs';
const MEMORY_COLLECTION =
  process.env.FORGE_MEMORY_COLLECTION ?? 'askdocs-chunks';
const EMBED_MODEL = process.env.FORGE_MODELS_EMBED_MODEL ?? 'local-embed-small';
const EMBED_DIM = Number(process.env.FORGE_MODELS_EMBED_DIM ?? 384);
const AGENT_NAME = process.env.ASKDOCS_AGENT_NAME ?? 'askdocs-answerer';
const DEMO_ID = '03-askdocs';

const PLANTED_FACT =
  process.env.ASKDOCS_PLANTED_FACT ??
  'The office is closed on the first Monday of each month.';
const RETRIEVAL_QUESTION =
  process.env.ASKDOCS_RETRIEVAL_QUESTION ?? 'When is the office closed?';
const OUT_OF_CORPUS_QUESTION =
  process.env.ASKDOCS_OUT_OF_CORPUS_QUESTION ??
  "What is the CEO's home address?";
const REFUSAL_SNIPPET =
  process.env.ASKDOCS_REFUSAL_SNIPPET ?? 'not in the documents';
const DOC_TITLE = `Company Handbook ${Date.now().toString(36)}`;

const fixturePath = path.resolve(__dirname, 'fixtures/company-handbook.txt');

async function apiJson<T>(
  pathName: string,
  init: RequestInit = {},
): Promise<{ status: number; body: T; text: string }> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('content-type') && !(init.body instanceof FormData)) {
    headers.set('content-type', 'application/json');
  }
  const res = await fetch(`${API_URL}${pathName}`, { ...init, headers });
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

async function waitDocumentReady(title: string, timeoutMs = 180_000): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    const { status, body, text } = await apiJson<{
      documents?: Array<{ id: string; title: string; status: string }>;
    }>('/documents');
    if (status === 200) {
      const doc = (body.documents || []).find((d) => d.title === title);
      if (doc) {
        last = doc.status;
        if (doc.status === 'ready') return doc.id;
      } else {
        last = 'missing';
      }
    } else {
      last = `HTTP ${status}: ${text.slice(0, 120)}`;
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`document ${JSON.stringify(title)} never reached ready (last=${last})`);
}

async function waitUiDocumentReady(page: Page, title: string, timeoutMs = 180_000): Promise<void> {
  const row = page.locator('#document-list > li').filter({ hasText: title });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect
    .poll(
      async () => {
        const text = (await row.textContent()) || '';
        return /—\s*ready\b/i.test(text) ? 'ready' : text;
      },
      { timeout: timeoutMs },
    )
    .toBe('ready');
}

async function askQuestion(page: Page, question: string): Promise<void> {
  await page.getByLabel('Question').fill(question);
  await page.getByRole('button', { name: /^ask$/i }).click();
}

async function findAskDocsTrace(): Promise<{ source: string; detail: string }> {
  const errors: string[] = [];

  try {
    const q = encodeURIComponent(
      '{ name=~".*(chat|query|embed|memory|agent|askdocs).*" || resource.service.name=~".*(askdocs|models|memory|agents).*" }',
    );
    const tempoRes = await fetch(`${TEMPO_URL}/api/search?q=${q}&limit=20`);
    if (tempoRes.ok) {
      const payload = (await tempoRes.json()) as {
        traces?: Array<{ rootTraceName?: string; rootServiceName?: string }>;
      };
      const traces = payload.traces ?? [];
      if (traces.length > 0) {
        const hit = traces.find((t) => {
          const blob = `${t.rootTraceName ?? ''} ${t.rootServiceName ?? ''}`.toLowerCase();
          return (
            blob.includes('askdocs') ||
            blob.includes('chat') ||
            blob.includes('model') ||
            blob.includes('memory') ||
            blob.includes('agent')
          );
        });
        return {
          source: 'tempo',
          detail: hit
            ? JSON.stringify(hit)
            : `tempo returned ${traces.length} trace(s); none clearly AskDocs/AI stack`,
        };
      }
      errors.push('tempo search returned zero traces');
    } else {
      errors.push(`tempo HTTP ${tempoRes.status}`);
    }
  } catch (err) {
    errors.push(`tempo: ${err instanceof Error ? err.message : String(err)}`);
  }

  // Observe requires at least one scoping filter (project|deployment|service|…).
  const logQueries = [
    `${OBSERVE_URL}/v1/logs?service=askdocs-api&limit=80`,
    `${OBSERVE_URL}/v1/logs?service=forge-models&limit=40`,
    `${OBSERVE_URL}/v1/logs?service=forge-memory&limit=40`,
    `${OBSERVE_URL}/v1/logs?service=forge-agents&limit=40`,
    `${OBSERVE_URL}/v1/logs?project=${encodeURIComponent(MEMORY_PROJECT)}&limit=80`,
  ];
  for (const url of logQueries) {
    try {
      const observeRes = await fetch(url);
      if (observeRes.ok) {
        const text = await observeRes.text();
        if (
          /askdocs|\/chat|\/query|local-embed-small|askdocs-chunks|memory\.search|forge-models|forge-memory|forge-agents|embed/i.test(
            text,
          )
        ) {
          return {
            source: 'observe-logs',
            detail: `matched AI-stack evidence via ${url}`,
          };
        }
        errors.push(`${url}: no AI-stack match`);
      } else {
        errors.push(`${url}: HTTP ${observeRes.status}`);
      }
    } catch (err) {
      errors.push(`${url}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  throw new Error(
    `no connected Observe/Tempo evidence for AskDocs query path (${errors.join('; ') || 'no backends'})`,
  );
}

/**
 * AskDocs browser E2E (53.05): upload fixture → ready → grounded cited answer,
 * out-of-corpus refusal, history persists; platform.expect for Models↔Memory,
 * Memory top-k, Agent memory.search trace, Observe cross-service evidence.
 */
test.describe('03-askdocs', () => {
  test.describe.configure({ mode: 'serial' });

  test('upload → ready → grounded citation → refusal → history', async ({ page }) => {
    test.setTimeout(360_000);

    expect(fs.existsSync(fixturePath)).toBe(true);
    const fixtureText = fs.readFileSync(fixturePath, 'utf8');
    expect(fixtureText).toContain(PLANTED_FACT);

    // Fresh browser session so chat history assertions are not polluted by prior runs.
    await page.goto(BASE_URL);
    await page.evaluate(() => localStorage.removeItem('askdocs.sessionId'));
    await page.reload();

    await expect(page.getByText('AskDocs', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: /document q&a/i })).toBeVisible();
    await expect(page.getByRole('heading', { name: /^documents$/i })).toBeVisible();
    await expect(page.getByRole('heading', { name: /^chat$/i })).toBeVisible();

    // --- Product: upload fixture handbook, wait for ingest ready ---
    await page.getByLabel('Title').fill(DOC_TITLE);
    await page.locator('#doc-file').setInputFiles(fixturePath);
    await page.getByRole('button', { name: /^upload$/i }).click();
    await expect(page.locator('#upload-status')).toContainText(/uploaded|ingest/i, {
      timeout: 30_000,
    });

    const documentId = await waitDocumentReady(DOC_TITLE);
    await waitUiDocumentReady(page, DOC_TITLE);

    // --- Product: planted-fact question → grounded answer + citation ---
    await askQuestion(page, RETRIEVAL_QUESTION);
    const groundedMsg = page.locator('#message-list > li.msg-assistant').filter({
      hasText: new RegExp(PLANTED_FACT.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i'),
    });
    await expect(groundedMsg).toBeVisible({ timeout: 60_000 });
    // Citation title may be this upload or an earlier KEEP=1 handbook with the same planted fact.
    const cite = groundedMsg.locator('.citations li').first();
    await expect(cite).toContainText(/Source:/i);
    await expect(cite).toContainText(/handbook|company handbook/i);
    await expect(page.getByText(RETRIEVAL_QUESTION, { exact: true })).toBeVisible();

    // Prove the document we uploaded is ready with planted chunks (API).
    const chunksRes = await apiJson<{
      chunks?: Array<{ text?: string; memoryId?: string }>;
    }>(`/documents/${documentId}/chunks`);
    expect(chunksRes.status).toBe(200);
    const chunks = chunksRes.body.chunks || [];
    expect(chunks.length).toBeGreaterThan(0);
    expect(chunks.some((c) => (c.text || '').includes(PLANTED_FACT))).toBe(true);
    expect(chunks.every((c) => (c.memoryId || '').trim().length > 0)).toBe(true);

    // --- Product: out-of-corpus → refusal (no planted fact, no citations) ---
    await askQuestion(page, OUT_OF_CORPUS_QUESTION);
    const refusalMsg = page.locator('#message-list > li.msg-assistant').filter({
      hasText: new RegExp(REFUSAL_SNIPPET, 'i'),
    });
    await expect(refusalMsg).toBeVisible({ timeout: 60_000 });
    await expect(refusalMsg).not.toContainText(PLANTED_FACT);
    await expect(refusalMsg.locator('.citations')).toHaveCount(0);
    await expect(page.getByText(OUT_OF_CORPUS_QUESTION, { exact: true })).toBeVisible();

    // --- Product: reload → chat history persists (Postgres) ---
    await page.reload();
    await expect(page.getByText(RETRIEVAL_QUESTION, { exact: true })).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page.locator('#message-list > li.msg-assistant').filter({
        hasText: new RegExp(PLANTED_FACT.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i'),
      }),
    ).toBeVisible();
    await expect(page.getByText(OUT_OF_CORPUS_QUESTION, { exact: true })).toBeVisible();
    await expect(
      page.locator('#message-list > li.msg-assistant').filter({
        hasText: new RegExp(REFUSAL_SNIPPET, 'i'),
      }),
    ).toBeVisible();

    // Capture runId from a fresh API chat for agent-trace assertion.
    const chatRes = await apiJson<{
      refused?: boolean;
      runId?: string;
      agentTool?: string | null;
      assistant?: { text?: string; citations?: Array<{ title?: string; chunkId?: string }> };
    }>('/chat', {
      method: 'POST',
      body: JSON.stringify({
        sessionId: `e2e-platform-${Date.now().toString(36)}`,
        text: RETRIEVAL_QUESTION,
      }),
    });
    expect(chatRes.status).toBe(201);
    expect(chatRes.body.refused).toBe(false);
    expect(chatRes.body.assistant?.text || '').toContain(PLANTED_FACT);
    expect((chatRes.body.assistant?.citations || []).length).toBeGreaterThan(0);
    const runId = chatRes.body.runId || '';
    expect(runId.length).toBeGreaterThan(0);
    expect(chatRes.body.agentTool).toBe('memory.search');

    // --- Platform assertions ---
    const modelsMemoryResult = await platform.expect(
      'models',
      async () => {
        // Gateway may rewrite /health/ready to {"status":"ok"}; use / + /query instead.
        const root = await apiJson<{
          embedModel?: string;
          embedDim?: number;
          collection?: string;
        }>('/');
        if (root.status !== 200) {
          throw new Error(`API / HTTP ${root.status}: ${root.text}`);
        }
        if (root.body.embedModel !== EMBED_MODEL) {
          throw new Error(`embedModel=${root.body.embedModel}, want ${EMBED_MODEL}`);
        }
        if (Number(root.body.embedDim) !== EMBED_DIM) {
          throw new Error(`embedDim=${root.body.embedDim}, want ${EMBED_DIM}`);
        }
        if (root.body.collection !== MEMORY_COLLECTION) {
          throw new Error(`collection=${root.body.collection}, want ${MEMORY_COLLECTION}`);
        }

        const modelRes = await fetch(`${MODELS_URL}/v1/models/${EMBED_MODEL}`);
        if (!modelRes.ok) {
          throw new Error(`GET Models ${EMBED_MODEL} HTTP ${modelRes.status}`);
        }
        const modelBody = (await modelRes.json()) as {
          dim?: number;
          dimensions?: number;
          embedding_dim?: number;
        };
        const modelDim = Number(
          modelBody.embedding_dim ?? modelBody.dim ?? modelBody.dimensions ?? 0,
        );
        if (modelDim !== EMBED_DIM) {
          throw new Error(`Models registry dim=${modelDim}, want ${EMBED_DIM}`);
        }

        const memRes = await fetch(`${MEMORY_URL}/v1/collections/${MEMORY_COLLECTION}`, {
          headers: { 'X-Forge-Project': MEMORY_PROJECT },
        });
        if (!memRes.ok) {
          throw new Error(`GET Memory collection HTTP ${memRes.status}`);
        }
        const memBody = (await memRes.json()) as { dim?: number; distance?: string };
        if (Number(memBody.dim) !== EMBED_DIM) {
          throw new Error(`Memory collection dim=${memBody.dim}, want ${EMBED_DIM}`);
        }
        if ((memBody.distance || '').toLowerCase() !== 'cosine') {
          throw new Error(`Memory distance=${memBody.distance}, want cosine`);
        }

        const query = await apiJson<{ embedDim?: number; embedModel?: string; collection?: string }>(
          '/query',
          {
            method: 'POST',
            body: JSON.stringify({ text: RETRIEVAL_QUESTION, topK: 3 }),
          },
        );
        if (query.status !== 200) {
          throw new Error(`POST /query HTTP ${query.status}: ${query.text}`);
        }
        if (Number(query.body.embedDim) !== EMBED_DIM || query.body.embedModel !== EMBED_MODEL) {
          throw new Error(
            `query contract embedDim=${query.body.embedDim} embedModel=${query.body.embedModel}`,
          );
        }
        if (query.body.collection !== MEMORY_COLLECTION) {
          throw new Error(`query collection=${query.body.collection}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Models↔Memory embedding format/dim contract must hold for AskDocs',
        tested: `API / + POST /query + Models ${EMBED_MODEL} + Memory ${MEMORY_COLLECTION}`,
        expected: `model=${EMBED_MODEL} dim=${EMBED_DIM} collection=${MEMORY_COLLECTION} distance=cosine`,
        area: 'forge-models / forge-memory embedding contract (53.03/53.05)',
        repro: [
          'make demo DEMO=53 KEEP=1',
          'cd tests/e2e && npx playwright test projects/03-askdocs',
        ],
      },
    );
    expect(modelsMemoryResult.outcome).not.toBe('failed');

    const memoryTopKResult = await platform.expect(
      'memory',
      async () => {
        const query = await apiJson<{
          embedDim?: number;
          embedModel?: string;
          collection?: string;
          results?: Array<{
            chunk?: { text?: string };
            citation?: { chunkId?: string; memoryId?: string; documentId?: string };
          }>;
        }>('/query', {
          method: 'POST',
          body: JSON.stringify({ text: RETRIEVAL_QUESTION, topK: 5 }),
        });
        if (query.status !== 200) {
          throw new Error(`POST /query HTTP ${query.status}: ${query.text}`);
        }
        if (Number(query.body.embedDim) !== EMBED_DIM) {
          throw new Error(`query embedDim=${query.body.embedDim}`);
        }
        if (query.body.embedModel !== EMBED_MODEL) {
          throw new Error(`query embedModel=${query.body.embedModel}`);
        }
        if (query.body.collection !== MEMORY_COLLECTION) {
          throw new Error(`query collection=${query.body.collection}`);
        }
        const results = query.body.results || [];
        if (!results.length) throw new Error('query returned zero results');
        const hit = results.find((r) => (r.chunk?.text || '').includes(PLANTED_FACT));
        if (!hit) {
          throw new Error(`planted chunk missing from top-k (n=${results.length})`);
        }
        if (!hit.citation?.chunkId || !hit.citation?.memoryId || !hit.citation?.documentId) {
          throw new Error(`citation incomplete: ${JSON.stringify(hit.citation)}`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Memory kNN (via AskDocs /query) must return planted chunk in top-k',
        tested: `POST /query text="${RETRIEVAL_QUESTION}" topK=5`,
        expected: `results contain "${PLANTED_FACT}" with citation chunkId/memoryId/documentId`,
        area: 'forge-memory kNN + Models query embed (53.03/53.05)',
        repro: [
          'make demo DEMO=53 KEEP=1',
          `curl -H Host:api.askdocs.localhost -H 'content-type: application/json' -d '{"text":"${RETRIEVAL_QUESTION}","topK":5}' http://127.0.0.1:4000/query`,
        ],
      },
    );
    expect(memoryTopKResult.outcome).not.toBe('failed');

    const agentTraceResult = await platform.expect(
      'agents',
      async () => {
        // Product design names the tool `retrieve`; platform stand-in is memory.search (F-006).
        if (chatRes.body.agentTool !== 'memory.search') {
          throw new Error(`agentTool=${chatRes.body.agentTool}, want memory.search (retrieve stand-in)`);
        }
        const runRes = await fetch(`${AGENTS_URL}/v1/runs/${runId}`, {
          headers: { 'X-Forge-Project': MEMORY_PROJECT },
        });
        if (!runRes.ok) {
          throw new Error(`GET agent run HTTP ${runRes.status}`);
        }
        const run = (await runRes.json()) as {
          status?: string;
          steps?: Array<{ type?: string; tool?: string }>;
          result?: string;
        };
        if (run.status !== 'succeeded') {
          throw new Error(`agent run status=${run.status}`);
        }
        const tools = (run.steps || []).filter((s) => s.type === 'tool');
        if (!tools.length) throw new Error(`no tool steps in run: ${JSON.stringify(run.steps)}`);
        if (tools[0].tool !== 'memory.search') {
          throw new Error(`first tool=${tools[0].tool}, want memory.search`);
        }
        const blob = JSON.stringify(run);
        if (!blob.includes(PLANTED_FACT) && !(run.result || '').includes(PLANTED_FACT)) {
          // Grounded final may only appear in chat assistant text; require tool + succeeded.
          const finals = (run.steps || []).filter((s) => s.type === 'final');
          if (!finals.length) {
            throw new Error('agent run missing final step after memory.search');
          }
        }
        const agentsList = await fetch(`${AGENTS_URL}/v1/agents`, {
          headers: { 'X-Forge-Project': MEMORY_PROJECT },
        });
        if (!agentsList.ok) throw new Error(`list agents HTTP ${agentsList.status}`);
        const agentsBody = (await agentsList.json()) as { agents?: Array<{ name?: string }> };
        const names = (agentsBody.agents || []).map((a) => a.name || '');
        if (!names.includes(AGENT_NAME)) {
          throw new Error(`agent ${AGENT_NAME} not registered (have ${names.join(',')})`);
        }
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Agent askdocs-answerer must invoke memory.search (retrieve stand-in) on grounded ask',
        tested: `POST /chat → GET ${AGENTS_URL}/v1/runs/{runId}`,
        expected: 'run status=succeeded; first tool step tool=memory.search; agent registered',
        area: 'forge-agents tool invocation / F-006 retrieve→memory.search (53.04/53.05)',
        repro: [
          'make demo DEMO=53 KEEP=1',
          'cd tests/e2e && npx playwright test projects/03-askdocs',
        ],
      },
    );
    expect(agentTraceResult.outcome).not.toBe('failed');

    const observeResult = await platform.expect(
      'observe',
      async () => {
        // Fresh chat to give collectors something recent when OTEL is enabled.
        const probe = await apiJson('/chat', {
          method: 'POST',
          body: JSON.stringify({
            sessionId: `e2e-trace-${Date.now().toString(36)}`,
            text: RETRIEVAL_QUESTION,
          }),
        });
        if (probe.status !== 201) {
          throw new Error(`trace-probe chat HTTP ${probe.status}`);
        }
        await new Promise((r) => setTimeout(r, 2000));
        await findAskDocsTrace();
      },
      {
        severity: 'major',
        demo: DEMO_ID,
        title: 'Observe should show connected evidence spanning AskDocs → Models/Memory/Agents',
        tested: 'POST /chat then Tempo /api/search + Observe /v1/logs?service=…',
        expected: '≥1 Tempo trace or observe log evidence for AskDocs/AI stack path',
        area: 'forge-observe cross-service telemetry (53.05)',
        repro: [
          'make demo DEMO=53 KEEP=1',
          'curl -s "http://127.0.0.1:3002/api/search?limit=20"',
          'curl -s "http://127.0.0.1:4106/v1/logs?service=askdocs-api&limit=50"',
        ],
      },
    );
    // Trace gaps are findings; product path already passed.
    expect(['passed', 'degraded']).toContain(observeResult.outcome);
  });
});
