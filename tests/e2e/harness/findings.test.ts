import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  loadFindings,
  outcomeForSeverity,
  platform,
  record,
  type FindingsPaths,
} from './findings';

function tempPaths(label: string): FindingsPaths & { dir: string } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `forge-findings-${label}-`));
  const markdownPath = path.join(dir, 'PLATFORM_FINDINGS.md');
  const jsonPath = path.join(dir, 'findings.json');
  // Leave markdown absent so record() seeds an empty doc and unit tests get F-001…
  // independently of the living docs/demo-projects/PLATFORM_FINDINGS.md counters.
  return { dir, markdownPath, jsonPath, today: '2026-07-24' };
}

test('outcomeForSeverity maps blocker→failed and major/minor→degraded', () => {
  assert.equal(outcomeForSeverity('blocker'), 'failed');
  assert.equal(outcomeForSeverity('major'), 'degraded');
  assert.equal(outcomeForSeverity('minor'), 'degraded');
});

test('recording a finding appends a well-formed block and increments counters', () => {
  const paths = tempPaths('record');
  try {
    const result = record(
      {
        service: 'forge-events',
        demo: '02-snapnote',
        severity: 'minor',
        title: 'sample finding for harness unit test',
        tested: 'publish then ack a message',
        expected: 'acked messages are not redelivered',
        actual: 'message redelivered after restart',
        evidence: {
          httpStatus: 200,
          method: 'POST',
          url: '/v1/publish',
          body: '{"ok":true}',
          logs: 'events: redelivery id=abc',
          traceId: 'trace-unit-1',
        },
        repro: ['make demo DEMO=52'],
      },
      paths,
    );

    assert.equal(result.appended, true);
    assert.equal(result.id, 'F-001');
    assert.equal(result.document.summary.total, 1);
    assert.equal(result.document.summary.minor, 1);
    assert.equal(result.document.summary.open, 1);
    assert.equal(result.document.byService['forge-events']?.minor, 1);
    assert.equal(result.document.byDemo['02-snapnote'], 1);

    const md = fs.readFileSync(paths.markdownPath!, 'utf8');
    assert.match(md, /### F-001 — sample finding for harness unit test/);
    assert.match(md, /\|\s*Severity\s*\|\s*minor\s*\|/);
    assert.match(md, /\|\s*Service\s*\|\s*forge-events\s*\|/);
    assert.match(md, /\|\s*Total findings\s*\|\s*1\s*\|/);
    assert.match(md, /\|\s*Minor\s*\|\s*1\s*\|/);
    assert.match(md, /\|\s*forge-events\s*\|\s*1\s*\|\s*0\s*\|\s*0\s*\|\s*1\s*\|/);
    assert.match(md, /\|\s*02-snapnote\s*\|\s*1\s*\|/);
    assert.doesNotMatch(md, /_No findings recorded yet/);
    assert.match(md, /trace-unit-1/);

    const json = JSON.parse(fs.readFileSync(paths.jsonPath!, 'utf8'));
    assert.equal(json.findings.length, 1);
    assert.equal(json.findings[0].id, 'F-001');
    assert.equal(json.summary.total, 1);
  } finally {
    fs.rmSync(paths.dir, { recursive: true, force: true });
  }
});

test('duplicate service+title does not double-append', () => {
  const paths = tempPaths('dedupe');
  try {
    const first = record(
      {
        service: 'events',
        demo: 'smoke',
        severity: 'major',
        title: 'duplicate title',
        actual: 'first',
      },
      paths,
    );
    assert.equal(first.appended, true);
    assert.equal(first.finding.service, 'forge-events');

    const second = record(
      {
        service: 'forge-events',
        demo: 'smoke',
        severity: 'blocker',
        title: 'duplicate title',
        actual: 'second should be ignored',
      },
      paths,
    );
    assert.equal(second.appended, false);
    assert.equal(second.id, first.id);

    const md = fs.readFileSync(paths.markdownPath!, 'utf8');
    assert.equal((md.match(/### F-001 — duplicate title/g) ?? []).length, 1);
    assert.doesNotMatch(md, /second should be ignored/);

    const doc = loadFindings(paths);
    assert.equal(doc.findings.length, 1);
    assert.equal(doc.summary.total, 1);
    assert.equal(doc.summary.major, 1);
    assert.equal(doc.summary.blocker, 0);
    assert.equal(doc.byDemo.smoke, 1);
  } finally {
    fs.rmSync(paths.dir, { recursive: true, force: true });
  }
});

test('platform.expect blocker vs major vs minor produce correct outcome + record', async () => {
  const paths = tempPaths('expect');
  try {
    const ok = await platform.expect(
      'observe',
      async () => {
        /* pass */
      },
      { paths, demo: 'smoke' },
    );
    assert.equal(ok.ok, true);
    assert.equal(ok.outcome, 'passed');
    assert.equal(ok.finding, undefined);

    const blocker = await platform.expect(
      'observe',
      async () => {
        throw new Error('no spans for POST /tasks');
      },
      {
        paths,
        demo: '01-taskflow',
        severity: 'blocker',
        title: 'no trace recorded for POST /tasks',
        evidence: { traceId: 'missing', httpStatus: 200 },
      },
    );
    assert.equal(blocker.ok, false);
    assert.equal(blocker.outcome, 'failed');
    assert.ok(blocker.finding?.appended);
    assert.equal(blocker.finding?.finding.service, 'forge-observe');
    assert.equal(blocker.finding?.finding.severity, 'blocker');

    const major = await platform.expect(
      'gateway',
      () => {
        throw new Error('Host:port matching regressed');
      },
      {
        paths,
        demo: 'smoke',
        severity: 'major',
        title: 'gateway host-port matching failed',
      },
    );
    assert.equal(major.ok, false);
    assert.equal(major.outcome, 'degraded');
    assert.equal(major.finding?.finding.severity, 'major');

    const minor = await platform.expect(
      'forge-control',
      async () => {
        throw Object.assign(new Error('missing request-id header'), {
          evidence: { logs: 'control access log without x-request-id' },
        });
      },
      {
        paths,
        demo: 'smoke',
        severity: 'minor',
        title: 'control omits request-id on error path',
      },
    );
    assert.equal(minor.ok, false);
    assert.equal(minor.outcome, 'degraded');
    assert.equal(minor.finding?.finding.severity, 'minor');
    assert.equal(
      minor.finding?.finding.evidence?.logs,
      'control access log without x-request-id',
    );

    const doc = loadFindings(paths);
    assert.equal(doc.summary.total, 3);
    assert.equal(doc.summary.blocker, 1);
    assert.equal(doc.summary.major, 1);
    assert.equal(doc.summary.minor, 1);
    assert.equal(doc.byService['forge-observe']?.blocker, 1);
    assert.equal(doc.byService['forge-gateway']?.major, 1);
    assert.equal(doc.byService['forge-control']?.minor, 1);
  } finally {
    fs.rmSync(paths.dir, { recursive: true, force: true });
  }
});

test('platform.expect does not throw — ordinary Playwright expect stays separate', async () => {
  const paths = tempPaths('no-throw');
  try {
    await assert.doesNotReject(() =>
      platform.expect(
        'events',
        () => {
          throw new Error('soft platform failure');
        },
        { paths, severity: 'minor', title: 'soft failure stays soft', demo: 'smoke' },
      ),
    );
  } finally {
    fs.rmSync(paths.dir, { recursive: true, force: true });
  }
});
