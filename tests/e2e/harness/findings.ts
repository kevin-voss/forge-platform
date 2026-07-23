import fs from 'node:fs';
import path from 'node:path';

/**
 * Findings collector — sole automated writer to PLATFORM_FINDINGS.md and
 * artifacts/findings.json. Also owns platform.expect, which routes platform
 * assertion failures into findings instead of hard Playwright failures.
 */

export type Severity = 'blocker' | 'major' | 'minor';

/** Product run outcome driven by platform-assertion severity. */
export type ProductOutcome = 'passed' | 'degraded' | 'failed';

export interface FindingEvidence {
  httpStatus?: number;
  body?: string;
  logs?: string;
  traceId?: string;
  screenshot?: string;
  method?: string;
  url?: string;
}

export interface FindingInput {
  service: string;
  demo: string;
  severity: Severity;
  title: string;
  tested?: string;
  expected?: string;
  actual?: string;
  evidence?: FindingEvidence;
  repro?: string[];
  area?: string;
  reproducible?: string;
  impact?: string;
  notes?: string;
}

export interface FindingRecord extends FindingInput {
  id: string;
  status: 'Open';
  firstSeen: string;
}

export interface FindingsSummary {
  total: number;
  open: number;
  blocker: number;
  major: number;
  minor: number;
}

export interface ServiceCounts {
  open: number;
  blocker: number;
  major: number;
  minor: number;
}

export interface FindingsDocument {
  findings: FindingRecord[];
  summary: FindingsSummary;
  byService: Record<string, ServiceCounts>;
  byDemo: Record<string, number>;
}

export interface RecordResult {
  id: string;
  appended: boolean;
  finding: FindingRecord;
  document: FindingsDocument;
}

export interface FindingsPaths {
  markdownPath?: string;
  jsonPath?: string;
  /** Override "today" for First seen (ISO date YYYY-MM-DD). */
  today?: string;
}

export interface PlatformExpectOptions {
  severity?: Severity;
  demo?: string;
  title?: string;
  tested?: string;
  expected?: string;
  actual?: string;
  evidence?: FindingEvidence;
  repro?: string[];
  area?: string;
  /** Paths for the findings writer (tests inject temp dirs). */
  paths?: FindingsPaths;
}

export interface PlatformExpectResult {
  ok: boolean;
  /** `passed` on success; `failed` for blocker; `degraded` for major/minor. */
  outcome: ProductOutcome;
  finding?: RecordResult;
  error?: Error;
}

const DEFAULT_DEMOS = [
  '01-taskflow',
  '02-snapnote',
  '03-askdocs',
  '04-orderpipe',
  '05-pulseboard',
] as const;

const EMPTY_PLACEHOLDER =
  '_No findings recorded yet. The first demo run appends entries below._';

function e2eRoot(): string {
  return path.resolve(__dirname, '..');
}

function repoRoot(): string {
  return path.resolve(e2eRoot(), '../..');
}

function defaultMarkdownPath(): string {
  return path.join(repoRoot(), 'docs/demo-projects/PLATFORM_FINDINGS.md');
}

function defaultJsonPath(): string {
  return path.join(e2eRoot(), 'artifacts/findings.json');
}

function todayIso(paths?: FindingsPaths): string {
  if (paths?.today) return paths.today;
  return new Date().toISOString().slice(0, 10);
}

/** Map severity to product-run outcome (blocker fails; others degrade). */
export function outcomeForSeverity(severity: Severity): ProductOutcome {
  return severity === 'blocker' ? 'failed' : 'degraded';
}

/**
 * Normalize a short service name (`observe`) to `forge-observe`.
 * Leaves `forge-*` and `platform` unchanged.
 */
export function normalizeService(service: string): string {
  const trimmed = service.trim();
  if (!trimmed) {
    throw new Error('service is required');
  }
  if (trimmed === 'platform' || trimmed.startsWith('forge-')) {
    return trimmed;
  }
  return `forge-${trimmed}`;
}

function dedupeKey(service: string, title: string): string {
  return `${service}\0${title}`;
}

function emptyDocument(): FindingsDocument {
  const byDemo: Record<string, number> = {};
  for (const demo of DEFAULT_DEMOS) {
    byDemo[demo] = 0;
  }
  return {
    findings: [],
    summary: { total: 0, open: 0, blocker: 0, major: 0, minor: 0 },
    byService: {},
    byDemo,
  };
}

function ensureArtifactsDir(jsonPath: string): void {
  fs.mkdirSync(path.dirname(jsonPath), { recursive: true });
}

function readJsonDocument(jsonPath: string): FindingsDocument | undefined {
  if (!fs.existsSync(jsonPath)) return undefined;
  try {
    const parsed = JSON.parse(fs.readFileSync(jsonPath, 'utf8')) as FindingsDocument;
    if (!parsed || !Array.isArray(parsed.findings)) return undefined;
    return {
      findings: parsed.findings,
      summary: parsed.summary ?? { total: 0, open: 0, blocker: 0, major: 0, minor: 0 },
      byService: parsed.byService ?? {},
      byDemo: { ...Object.fromEntries(DEFAULT_DEMOS.map((d) => [d, 0])), ...parsed.byDemo },
    };
  } catch {
    return undefined;
  }
}

/** Drop HTML comments so template examples inside `<!-- ... -->` are ignored. */
function stripHtmlComments(markdown: string): string {
  return markdown.replace(/<!--[\s\S]*?-->/g, '');
}

/** Parse existing F-NNN ids and service+title pairs from markdown. */
function parseMarkdownIndex(markdown: string): {
  maxId: number;
  keys: Set<string>;
  records: Array<{ id: string; service: string; title: string; severity: Severity }>;
} {
  const keys = new Set<string>();
  const records: Array<{
    id: string;
    service: string;
    title: string;
    severity: Severity;
  }> = [];
  let maxId = 0;

  const searchable = stripHtmlComments(markdown);
  const headingRe = /^### (F-(\d+)) — (.+)\s*$/gm;
  let match: RegExpExecArray | null;
  while ((match = headingRe.exec(searchable)) !== null) {
    const id = match[1];
    const num = Number(match[2]);
    const title = match[3].trim();
    if (num > maxId) maxId = num;

    const slice = searchable.slice(match.index, match.index + 2000);
    const serviceMatch = slice.match(/\|\s*Service\s*\|\s*([^|\n]+)\|/);
    const severityMatch = slice.match(/\|\s*Severity\s*\|\s*(blocker|major|minor)\s*\|/i);
    const service = (serviceMatch?.[1] ?? '').trim();
    const severity = (severityMatch?.[1]?.toLowerCase() ?? 'minor') as Severity;
    if (service && title) {
      keys.add(dedupeKey(service, title));
      records.push({ id, service, title, severity });
    }
  }

  return { maxId, keys, records };
}

function recomputeCounters(findings: FindingRecord[]): Omit<FindingsDocument, 'findings'> {
  const summary: FindingsSummary = {
    total: findings.length,
    open: 0,
    blocker: 0,
    major: 0,
    minor: 0,
  };
  const byService: Record<string, ServiceCounts> = {};
  const byDemo: Record<string, number> = {};
  for (const demo of DEFAULT_DEMOS) {
    byDemo[demo] = 0;
  }

  for (const f of findings) {
    if (f.status === 'Open') summary.open += 1;
    summary[f.severity] += 1;

    const svc = byService[f.service] ?? { open: 0, blocker: 0, major: 0, minor: 0 };
    if (f.status === 'Open') svc.open += 1;
    svc[f.severity] += 1;
    byService[f.service] = svc;

    byDemo[f.demo] = (byDemo[f.demo] ?? 0) + 1;
  }

  return { summary, byService, byDemo };
}

function loadDocument(paths: FindingsPaths): {
  markdown: string;
  markdownPath: string;
  jsonPath: string;
  document: FindingsDocument;
  index: ReturnType<typeof parseMarkdownIndex>;
} {
  const markdownPath = paths.markdownPath ?? defaultMarkdownPath();
  const jsonPath = paths.jsonPath ?? defaultJsonPath();

  let markdown: string;
  if (fs.existsSync(markdownPath)) {
    markdown = fs.readFileSync(markdownPath, 'utf8');
  } else {
    markdown = seedMarkdown();
  }

  const index = parseMarkdownIndex(markdown);
  const fromJson = readJsonDocument(jsonPath);

  let findings: FindingRecord[];
  if (fromJson && fromJson.findings.length > 0) {
    findings = fromJson.findings;
  } else if (index.records.length > 0) {
    // Reconstruct minimal records from markdown when JSON is missing.
    findings = index.records.map((r) => ({
      id: r.id,
      status: 'Open' as const,
      service: r.service,
      demo: 'unknown',
      severity: r.severity,
      title: r.title,
      firstSeen: todayIso(paths),
    }));
  } else {
    findings = [];
  }

  const counters = recomputeCounters(findings);
  return {
    markdown,
    markdownPath,
    jsonPath,
    document: { findings, ...counters },
    index,
  };
}

function seedMarkdown(): string {
  const demoRows = DEFAULT_DEMOS.map((d) => `| ${d} | 0 |`).join('\n');
  return `# Platform findings

Single, living record of **platform bugs and contract mismatches** surfaced by the demo-project
E2E track.

Machine-readable mirror: \`tests/e2e/artifacts/findings.json\`.

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
${demoRows}

---

## Findings

${EMPTY_PLACEHOLDER}
`;
}

function formatEvidence(evidence?: FindingEvidence): string {
  if (!evidence) {
    return '- _(none captured)_';
  }
  const lines: string[] = [];
  if (evidence.httpStatus !== undefined || evidence.method || evidence.url || evidence.body) {
    const method = evidence.method ?? 'GET';
    const url = evidence.url ?? '<url>';
    const status = evidence.httpStatus !== undefined ? String(evidence.httpStatus) : '?';
    const body =
      evidence.body !== undefined
        ? evidence.body.length > 200
          ? `${evidence.body.slice(0, 200)}…`
          : evidence.body
        : '';
    lines.push(
      `- HTTP: \`${method} ${url}\` → \`${status}\`${body ? ` body: \`${body.replace(/`/g, "'")}\`` : ''}`,
    );
  }
  if (evidence.logs) {
    lines.push(`- Logs: \`${evidence.logs.replace(/`/g, "'")}\``);
  }
  if (evidence.traceId) {
    lines.push(`- Trace id: \`${evidence.traceId}\``);
  }
  if (evidence.screenshot) {
    lines.push(`- Artifact: \`${evidence.screenshot}\``);
  }
  return lines.length > 0 ? lines.join('\n') : '- _(none captured)_';
}

function formatRepro(repro?: string[]): string {
  const steps =
    repro && repro.length > 0
      ? repro.join('\n')
      : ['make dev', '# re-run the demo / harness check that surfaced this finding'].join('\n');
  return ['```bash', steps, '```'].join('\n');
}

function formatFindingBlock(finding: FindingRecord): string {
  const impact =
    finding.impact ??
    (finding.severity === 'blocker'
      ? 'Demo marked **failed**; suite exits non-zero when this product is selected.'
      : finding.severity === 'major'
        ? 'Demo marked **degraded**; run continues.'
        : 'Recorded; run passes.');

  const lines = [
    `### ${finding.id} — ${finding.title}`,
    '',
    '| Field | Value |',
    '|---|---|',
    `| Status | ${finding.status} |`,
    `| Severity | ${finding.severity} |`,
    `| Service | ${finding.service} |`,
    `| Area / contract | ${finding.area ?? '—'} |`,
    `| Found by demo | ${finding.demo} |`,
    `| First seen | ${finding.firstSeen} |`,
    `| Reproducible | ${finding.reproducible ?? 'always'} |`,
    '',
    '**What we tested**',
    finding.tested?.trim() || '_(not specified)_',
    '',
    '**Expected (per spec/contract)**',
    finding.expected?.trim() || '_(not specified)_',
    '',
    '**Actual**',
    finding.actual?.trim() || '_(not specified)_',
    '',
    '**Evidence**',
    formatEvidence(finding.evidence),
    '',
    '**Reproduce**',
    formatRepro(finding.repro),
    '',
    '**Impact on demo**',
    impact,
  ];

  if (finding.notes?.trim()) {
    lines.push('', '**Suspected component / notes** (optional)', finding.notes.trim());
  }

  return lines.join('\n');
}

function renderSummaryTable(summary: FindingsSummary): string {
  return [
    '| Metric | Count |',
    '|---|---|',
    `| Total findings | ${summary.total} |`,
    `| Open | ${summary.open} |`,
    `| Blocker | ${summary.blocker} |`,
    `| Major | ${summary.major} |`,
    `| Minor | ${summary.minor} |`,
  ].join('\n');
}

function renderByServiceTable(byService: Record<string, ServiceCounts>): string {
  const services = Object.keys(byService).sort();
  const header = [
    '| Service | Open | Blocker | Major | Minor |',
    '|---|--:|--:|--:|--:|',
  ];
  if (services.length === 0) {
    return [...header, '| _(none yet)_ | 0 | 0 | 0 | 0 |'].join('\n');
  }
  const rows = services.map((svc) => {
    const c = byService[svc];
    return `| ${svc} | ${c.open} | ${c.blocker} | ${c.major} | ${c.minor} |`;
  });
  return [...header, ...rows].join('\n');
}

function renderByDemoTable(byDemo: Record<string, number>): string {
  const demos = new Set<string>([...DEFAULT_DEMOS, ...Object.keys(byDemo)]);
  const ordered = [
    ...DEFAULT_DEMOS.filter((d) => demos.has(d)),
    ...[...demos].filter((d) => !(DEFAULT_DEMOS as readonly string[]).includes(d)).sort(),
  ];
  const header = ['| Demo | Findings |', '|---|--:|'];
  const rows = ordered.map((d) => `| ${d} | ${byDemo[d] ?? 0} |`);
  return [...header, ...rows].join('\n');
}

function replaceSection(
  markdown: string,
  heading: string,
  nextTable: string,
): string {
  // Replace from `## <heading>` through the table that follows (until blank line + ## or ---).
  const re = new RegExp(
    `(## ${escapeRegExp(heading)}\\n\\n)([\\s\\S]*?)(?=\\n## |\\n---\\n)`,
  );
  if (!re.test(markdown)) {
    throw new Error(`PLATFORM_FINDINGS.md missing section: ${heading}`);
  }
  return markdown.replace(re, `$1${nextTable}\n`);
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function appendFindingToMarkdown(
  markdown: string,
  block: string,
  document: FindingsDocument,
): string {
  let next = replaceSection(markdown, 'Summary', renderSummaryTable(document.summary));
  next = replaceSection(next, 'By service', renderByServiceTable(document.byService));
  next = replaceSection(next, 'By demo', renderByDemoTable(document.byDemo));

  if (next.includes(EMPTY_PLACEHOLDER)) {
    next = next.replace(EMPTY_PLACEHOLDER, `${block}\n`);
  } else {
    const findingsIdx = next.indexOf('## Findings');
    if (findingsIdx < 0) {
      throw new Error('PLATFORM_FINDINGS.md missing ## Findings section');
    }
    // Append after existing content; keep trailing newline.
    const trimmed = next.replace(/\s*$/, '');
    next = `${trimmed}\n\n${block}\n`;
  }

  return next;
}

function nextFindingId(maxId: number): string {
  return `F-${String(maxId + 1).padStart(3, '0')}`;
}

/**
 * Append a finding to PLATFORM_FINDINGS.md and merge into findings.json.
 * Dedupes by `service+title` (no double-append).
 */
export function record(input: FindingInput, paths: FindingsPaths = {}): RecordResult {
  if (!input.title?.trim()) {
    throw new Error('title is required');
  }
  if (!input.demo?.trim()) {
    throw new Error('demo is required');
  }
  if (!['blocker', 'major', 'minor'].includes(input.severity)) {
    throw new Error(`invalid severity: ${input.severity}`);
  }

  const service = normalizeService(input.service);
  const title = input.title.trim();
  const loaded = loadDocument(paths);
  const key = dedupeKey(service, title);

  const existing = loaded.document.findings.find(
    (f) => dedupeKey(f.service, f.title) === key,
  );
  if (existing || loaded.index.keys.has(key)) {
    const finding: FindingRecord = existing ?? {
      ...input,
      id: 'F-000',
      status: 'Open',
      service,
      demo: input.demo.trim(),
      title,
      firstSeen: todayIso(paths),
    };
    return {
      id: finding.id,
      appended: false,
      finding,
      document: loaded.document,
    };
  }

  const id = nextFindingId(
    Math.max(
      loaded.index.maxId,
      ...loaded.document.findings.map((f) => Number(f.id.replace(/^F-/, '')) || 0),
    ),
  );

  const finding: FindingRecord = {
    ...input,
    id,
    status: 'Open',
    service,
    demo: input.demo.trim(),
    title,
    firstSeen: todayIso(paths),
  };

  const findings = [...loaded.document.findings, finding];
  const counters = recomputeCounters(findings);
  const document: FindingsDocument = { findings, ...counters };

  const block = formatFindingBlock(finding);
  const markdown = appendFindingToMarkdown(loaded.markdown, block, document);

  fs.mkdirSync(path.dirname(loaded.markdownPath), { recursive: true });
  fs.writeFileSync(loaded.markdownPath, markdown, 'utf8');

  ensureArtifactsDir(loaded.jsonPath);
  fs.writeFileSync(loaded.jsonPath, `${JSON.stringify(document, null, 2)}\n`, 'utf8');

  return { id, appended: true, finding, document };
}

function evidenceFromError(err: unknown): FindingEvidence | undefined {
  if (!err || typeof err !== 'object') return undefined;
  const e = err as { evidence?: FindingEvidence };
  return e.evidence;
}

/**
 * Platform assertion wrapper. On throw → findings.record(...) and return a
 * non-ok result with severity-driven outcome. Does **not** hard-fail the suite
 * (unlike Playwright `expect`).
 */
export async function platformExpect(
  service: string,
  fn: () => void | Promise<void>,
  options: PlatformExpectOptions = {},
): Promise<PlatformExpectResult> {
  const severity: Severity = options.severity ?? 'major';
  try {
    await fn();
    return { ok: true, outcome: 'passed' };
  } catch (err) {
    const error = err instanceof Error ? err : new Error(String(err));
    const title =
      options.title?.trim() ||
      truncateTitle(error.message) ||
      `Platform assertion failed for ${normalizeService(service)}`;

    const result = record(
      {
        service: normalizeService(service),
        demo: options.demo ?? 'harness',
        severity,
        title,
        tested: options.tested ?? `platform.expect('${service}', …)`,
        expected: options.expected ?? 'platform behaviour per contract / assertion',
        actual: options.actual ?? error.message,
        evidence: options.evidence ?? evidenceFromError(err),
        repro: options.repro,
        area: options.area,
        impact:
          severity === 'blocker'
            ? 'Demo marked **failed**; suite exits non-zero when this product is selected.'
            : 'Demo marked **degraded**; run continues.',
      },
      options.paths ?? {},
    );

    return {
      ok: false,
      outcome: outcomeForSeverity(severity),
      finding: result,
      error,
    };
  }
}

function truncateTitle(message: string, max = 80): string {
  const oneLine = message.replace(/\s+/g, ' ').trim();
  if (!oneLine) return '';
  return oneLine.length <= max ? oneLine : `${oneLine.slice(0, max - 1)}…`;
}

/** Namespace matching the design doc: `platform.expect(service, fn, opts)`. */
export const platform = {
  expect: platformExpect,
};

/** Test helper: load the current findings document from disk. */
export function loadFindings(paths: FindingsPaths = {}): FindingsDocument {
  return loadDocument(paths).document;
}
