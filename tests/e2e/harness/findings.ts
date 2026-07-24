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

/** One row in the PLATFORM_FINDINGS.md triage list (56.03). */
export interface TriageEntry {
  id: string;
  severity: Severity;
  service: string;
  title: string;
  demo: string;
  /** Service owner / area / notes used for hand-off. */
  suspectedComponent: string;
  missingEvidence: boolean;
}

export interface ConsolidateResult {
  document: FindingsDocument;
  triage: TriageEntry[];
  missingEvidenceIds: string[];
  markdownPath: string;
  jsonPath: string;
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

/** Lower rank = higher priority (blocker first). */
export const SEVERITY_RANK: Record<Severity, number> = {
  blocker: 0,
  major: 1,
  minor: 2,
};

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

/** Map severity to sort rank (blocker=0 … minor=2). */
export function severityRank(severity: Severity): number {
  return SEVERITY_RANK[severity] ?? 99;
}

/**
 * Normalize "Found by demo" cell values like `01-taskflow (step 51.03)` → `01-taskflow`.
 */
export function normalizeDemo(demo: string): string {
  const trimmed = demo.trim();
  if (!trimmed) return trimmed;
  const known = trimmed.match(/^(0[1-5]-[a-z0-9-]+)/i);
  if (known) return known[1];
  return trimmed;
}

/** True when the finding carries at least one machine-verifiable evidence artifact. */
export function hasEvidence(finding: FindingRecord): boolean {
  const e = finding.evidence;
  if (!e) return false;
  if (e.httpStatus !== undefined) return true;
  if (e.traceId?.trim()) return true;
  if (e.screenshot?.trim()) return true;
  if (e.logs?.trim() && e.logs.trim() !== '_(none captured)_') return true;
  if (e.body?.trim()) return true;
  if (e.method?.trim() || e.url?.trim()) return true;
  return false;
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

interface MarkdownFindingIndex {
  maxId: number;
  keys: Set<string>;
  records: FindingRecord[];
  /** Raw `### F-NNN — …` blocks keyed by id (preserves human prose on consolidate). */
  blocks: Map<string, string>;
}

function fieldFromTable(slice: string, field: string): string {
  const re = new RegExp(
    `\\|\\s*${escapeRegExp(field)}\\s*\\|\\s*([^|\\n]+)\\|`,
    'i',
  );
  return (slice.match(re)?.[1] ?? '').trim();
}

function sectionBody(slice: string, heading: string): string {
  const re = new RegExp(
    `\\*\\*${escapeRegExp(heading)}\\*\\*\\n([\\s\\S]*?)(?=\\n\\*\\*|\\n### |$)`,
  );
  return (slice.match(re)?.[1] ?? '').trim();
}

function expectedFromMarkdown(slice: string): string | undefined {
  const match = slice.match(
    /\*\*Expected[^*]*\*\*\n([\s\S]*?)(?=\n\*\*|\n### |$)/,
  );
  const body = match?.[1]?.trim();
  return body || undefined;
}

function evidenceFromMarkdown(evidenceBody: string): FindingEvidence | undefined {
  const body = evidenceBody.trim();
  if (
    !body ||
    body === '- _(none captured)_' ||
    body === '_(none captured)_' ||
    body === '- _(none)_'
  ) {
    return undefined;
  }

  const evidence: FindingEvidence = {};
  const http = body.match(
    /HTTP:\s*`([^`]+)`\s*→\s*`([^`]+)`(?:\s*body:\s*`([^`]*)`)?/i,
  );
  if (http) {
    const left = http[1].trim();
    const status = http[2].trim();
    const space = left.indexOf(' ');
    if (space > 0) {
      evidence.method = left.slice(0, space);
      evidence.url = left.slice(space + 1);
    } else {
      evidence.url = left;
    }
    const n = Number(status);
    if (!Number.isNaN(n)) evidence.httpStatus = n;
    if (http[3] !== undefined) evidence.body = http[3];
  }
  const logs = body.match(/Logs:\s*`([^`]+)`/i);
  if (logs) evidence.logs = logs[1];
  const trace = body.match(/Trace id:\s*`([^`]+)`/i);
  if (trace) evidence.traceId = trace[1];
  const artifact = body.match(/Artifact:\s*`([^`]+)`/i);
  if (artifact) evidence.screenshot = artifact[1];

  // Free-form evidence bullets (path citations, OpenAPI refs) still count.
  if (!hasEvidence({ evidence } as FindingRecord)) {
    evidence.logs = body.replace(/\n+/g, ' ').slice(0, 500);
  }
  return evidence;
}

/** Parse existing F-NNN blocks, counters fields, and raw markdown bodies. */
function parseMarkdownIndex(markdown: string): MarkdownFindingIndex {
  const keys = new Set<string>();
  const records: FindingRecord[] = [];
  const blocks = new Map<string, string>();
  let maxId = 0;

  const searchable = stripHtmlComments(markdown);
  const headingRe = /^### (F-(\d+)) — (.+)\s*$/gm;
  const matches: Array<{
    id: string;
    num: number;
    title: string;
    index: number;
  }> = [];
  let match: RegExpExecArray | null;
  while ((match = headingRe.exec(searchable)) !== null) {
    matches.push({
      id: match[1],
      num: Number(match[2]),
      title: match[3].trim(),
      index: match.index,
    });
  }

  for (let i = 0; i < matches.length; i += 1) {
    const current = matches[i];
    if (current.num > maxId) maxId = current.num;
    const end = i + 1 < matches.length ? matches[i + 1].index : searchable.length;
    const rawBlock = searchable.slice(current.index, end).replace(/\s*$/, '');
    blocks.set(current.id, rawBlock);

    const slice = rawBlock;
    const service = fieldFromTable(slice, 'Service');
    const severityRaw = fieldFromTable(slice, 'Severity').toLowerCase();
    const severity = (
      severityRaw === 'blocker' || severityRaw === 'major' || severityRaw === 'minor'
        ? severityRaw
        : 'minor'
    ) as Severity;
    const statusRaw = fieldFromTable(slice, 'Status');
    const demoRaw = fieldFromTable(slice, 'Found by demo');
    const area = fieldFromTable(slice, 'Area / contract');
    const firstSeen = fieldFromTable(slice, 'First seen') || todayIso();
    const reproducible = fieldFromTable(slice, 'Reproducible') || undefined;
    const evidenceBody = sectionBody(slice, 'Evidence');
    const notes =
      sectionBody(slice, 'Suspected component / notes') ||
      sectionBody(slice, 'Suggested platform fix') ||
      undefined;

    if (service && current.title) {
      keys.add(dedupeKey(service, current.title));
      records.push({
        id: current.id,
        status: statusRaw.toLowerCase() === 'open' || !statusRaw ? 'Open' : 'Open',
        service,
        demo: normalizeDemo(demoRaw || 'unknown'),
        severity,
        title: current.title,
        firstSeen,
        area: area || undefined,
        tested: sectionBody(slice, 'What we tested') || undefined,
        expected: expectedFromMarkdown(slice),
        actual: sectionBody(slice, 'Actual') || undefined,
        evidence: evidenceFromMarkdown(evidenceBody),
        impact: sectionBody(slice, 'Impact on demo') || sectionBody(slice, 'Impact') || undefined,
        notes,
        reproducible,
      });
    }
  }

  return { maxId, keys, records, blocks };
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

    const demo = normalizeDemo(f.demo);
    byDemo[demo] = (byDemo[demo] ?? 0) + 1;
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
    // Prefer JSON records; enrich missing demo/notes/evidence from markdown parse.
    const byId = new Map(index.records.map((r) => [r.id, r]));
    findings = fromJson.findings.map((f) => {
      const md = byId.get(f.id);
      if (!md) return { ...f, demo: normalizeDemo(f.demo) };
      return {
        ...md,
        ...f,
        demo: normalizeDemo(f.demo || md.demo),
        evidence: hasEvidence(f) ? f.evidence : md.evidence,
        notes: f.notes ?? md.notes,
        area: f.area ?? md.area,
      };
    });
  } else if (index.records.length > 0) {
    findings = index.records;
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

function findingIdNumber(id: string): number {
  return Number(id.replace(/^F-/, '')) || 0;
}

/**
 * Keep one finding per `service+title`. Prefer higher severity, then evidence,
 * then lower id (earlier first-seen).
 */
export function dedupeFindings(findings: FindingRecord[]): FindingRecord[] {
  const best = new Map<string, FindingRecord>();
  for (const raw of findings) {
    const finding: FindingRecord = {
      ...raw,
      demo: normalizeDemo(raw.demo),
      service: raw.service.trim(),
      title: raw.title.trim(),
    };
    const key = dedupeKey(finding.service, finding.title);
    const prev = best.get(key);
    if (!prev) {
      best.set(key, finding);
      continue;
    }
    const rankDiff = severityRank(finding.severity) - severityRank(prev.severity);
    if (rankDiff < 0) {
      best.set(key, finding);
      continue;
    }
    if (rankDiff > 0) continue;
    if (hasEvidence(finding) && !hasEvidence(prev)) {
      best.set(key, finding);
      continue;
    }
    if (!hasEvidence(finding) && hasEvidence(prev)) continue;
    if (findingIdNumber(finding.id) < findingIdNumber(prev.id)) {
      best.set(key, finding);
    }
  }
  return [...best.values()];
}

/** Rank findings: blocker → major → minor, then by id. */
export function rankFindings(findings: FindingRecord[]): FindingRecord[] {
  return [...findings].sort((a, b) => {
    const bySev = severityRank(a.severity) - severityRank(b.severity);
    if (bySev !== 0) return bySev;
    return findingIdNumber(a.id) - findingIdNumber(b.id);
  });
}

/** Build triage rows (blocker first) with evidence-gap flags. */
export function buildTriage(findings: FindingRecord[]): TriageEntry[] {
  return rankFindings(findings).map((f) => ({
    id: f.id,
    severity: f.severity,
    service: f.service,
    title: f.title,
    demo: normalizeDemo(f.demo),
    suspectedComponent: (f.notes?.trim() || f.area?.trim() || f.service).replace(
      /\n+/g,
      ' ',
    ),
    missingEvidence: !hasEvidence(f),
  }));
}

function renderTriageSection(triage: TriageEntry[]): string {
  const header = [
    '## Triage',
    '',
    'Ranked hand-off list: **blocker** findings first, then major, then minor.',
    'Service owner is the primary fix target; suspected component carries area/notes.',
    '',
    '| # | ID | Severity | Service owner | Suspected component | Demo | Evidence | Title |',
    '|--:|---|---|---|---|---|---|---|',
  ];
  if (triage.length === 0) {
    return [...header, '| — | — | — | — | — | — | — | _(none)_ |'].join('\n');
  }
  const cell = (value: string, max: number): string => {
    const flat = value.replace(/\|/g, '/').replace(/\s+/g, ' ').trim();
    return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
  };
  const rows = triage.map((t, i) => {
    const evidence = t.missingEvidence ? '⚠ missing' : 'ok';
    return (
      `| ${i + 1} | ${t.id} | ${t.severity} | ${cell(t.service, 40)} | ` +
      `${cell(t.suspectedComponent, 80)} | ${cell(t.demo, 24)} | ${evidence} | ` +
      `${cell(t.title, 60)} |`
    );
  });
  const missing = triage.filter((t) => t.missingEvidence).map((t) => t.id);
  const gaps =
    missing.length > 0
      ? [
          '',
          '### Evidence gaps',
          '',
          'Findings missing machine-verifiable evidence (template requirement): ' +
            missing.map((id) => `\`${id}\``).join(', ') +
            '.',
        ]
      : ['', '### Evidence gaps', '', '_All findings include evidence._'];
  return [...header, ...rows, ...gaps].join('\n');
}

function renderFindingsSection(
  ranked: FindingRecord[],
  blocks: Map<string, string>,
): string {
  if (ranked.length === 0) {
    return EMPTY_PLACEHOLDER;
  }
  return ranked
    .map((f) => blocks.get(f.id) ?? formatFindingBlock(f))
    .join('\n\n');
}

function replaceOrInsertTriage(markdown: string, triageMd: string): string {
  if (/^## Triage\s*$/m.test(markdown)) {
    return markdown.replace(
      /## Triage\n[\s\S]*?(?=\n## Findings\n)/,
      `${triageMd}\n\n`,
    );
  }
  // Insert triage immediately before ## Findings.
  if (!/^## Findings\s*$/m.test(markdown)) {
    throw new Error('PLATFORM_FINDINGS.md missing ## Findings section');
  }
  return markdown.replace(/\n## Findings\n/, `\n${triageMd}\n\n## Findings\n`);
}

function replaceFindingsSection(markdown: string, body: string): string {
  const re = /(## Findings\n\n)([\s\S]*)$/;
  if (!re.test(markdown)) {
    throw new Error('PLATFORM_FINDINGS.md missing ## Findings section');
  }
  const trimmedBody = body.replace(/\s*$/, '');
  return markdown.replace(re, `$1${trimmedBody}\n`);
}

function writeConsolidatedMarkdown(
  markdown: string,
  document: FindingsDocument,
  triage: TriageEntry[],
  blocks: Map<string, string>,
): string {
  let next = replaceSection(markdown, 'Summary', renderSummaryTable(document.summary));
  next = replaceSection(next, 'By service', renderByServiceTable(document.byService));
  next = replaceSection(next, 'By demo', renderByDemoTable(document.byDemo));
  next = replaceOrInsertTriage(next, renderTriageSection(triage));
  next = replaceFindingsSection(next, renderFindingsSection(document.findings, blocks));
  return next;
}

/**
 * Consolidation mode (56.03): dedupe by service+title, rank by severity,
 * refresh summary tables, emit triage list, flag evidence-less entries.
 * Rewrites PLATFORM_FINDINGS.md + findings.json.
 */
export function consolidate(paths: FindingsPaths = {}): ConsolidateResult {
  const loaded = loadDocument(paths);
  const deduped = dedupeFindings(loaded.document.findings);
  const ranked = rankFindings(deduped);
  const counters = recomputeCounters(ranked);
  const document: FindingsDocument = { findings: ranked, ...counters };
  const triage = buildTriage(ranked);
  const missingEvidenceIds = triage
    .filter((t) => t.missingEvidence)
    .map((t) => t.id);

  // Preserve human-authored bodies; synthesize blocks for JSON-only findings.
  const blocks = new Map(loaded.index.blocks);
  for (const f of ranked) {
    if (!blocks.has(f.id)) {
      blocks.set(f.id, formatFindingBlock(f));
    }
  }

  const markdown = writeConsolidatedMarkdown(
    loaded.markdown.includes('## Summary')
      ? loaded.markdown
      : seedMarkdown(),
    document,
    triage,
    blocks,
  );

  fs.mkdirSync(path.dirname(loaded.markdownPath), { recursive: true });
  fs.writeFileSync(loaded.markdownPath, markdown, 'utf8');
  ensureArtifactsDir(loaded.jsonPath);
  fs.writeFileSync(loaded.jsonPath, `${JSON.stringify(document, null, 2)}\n`, 'utf8');

  return {
    document,
    triage,
    missingEvidenceIds,
    markdownPath: loaded.markdownPath,
    jsonPath: loaded.jsonPath,
  };
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

/** CLI: `node harness/findings.js --consolidate` regenerates PLATFORM_FINDINGS.md. */
function main(argv: string[] = process.argv.slice(2)): void {
  if (!argv.includes('--consolidate')) {
    process.stderr.write(
      'usage: node harness/findings.js --consolidate\n',
    );
    process.exitCode = 2;
    return;
  }
  const result = consolidate();
  process.stdout.write(
    `[findings] consolidated ${result.document.summary.total} findings` +
      ` (blocker=${result.document.summary.blocker}` +
      ` major=${result.document.summary.major}` +
      ` minor=${result.document.summary.minor})` +
      ` missingEvidence=${result.missingEvidenceIds.length}` +
      `\n[findings] wrote ${result.markdownPath}\n` +
      `[findings] wrote ${result.jsonPath}\n`,
  );
}

if (require.main === module) {
  try {
    main();
  } catch (err) {
    process.stderr.write(
      `[findings] fatal: ${err instanceof Error ? err.stack ?? err.message : String(err)}\n`,
    );
    process.exitCode = 1;
  }
}
