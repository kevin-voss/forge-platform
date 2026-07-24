import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  CI_ARTIFACT_CORE_FILES,
  shouldStageCiArtifacts,
  stageCiArtifacts,
} from './artifacts';

test('shouldStageCiArtifacts honors CI=1 and CI=true', () => {
  assert.equal(shouldStageCiArtifacts({}), false);
  assert.equal(shouldStageCiArtifacts({ CI: '0' }), false);
  assert.equal(shouldStageCiArtifacts({ CI: '1' }), true);
  assert.equal(shouldStageCiArtifacts({ CI: 'true' }), true);
});

test('stageCiArtifacts copies PLATFORM_FINDINGS.md and writes upload-manifest.json', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'forge-ci-artifacts-'));
  const findingsSrc = path.join(dir, 'PLATFORM_FINDINGS.md');
  const artifactsDir = path.join(dir, 'artifacts');
  try {
    fs.writeFileSync(findingsSrc, '# findings\n', 'utf8');
    fs.mkdirSync(artifactsDir, { recursive: true });
    fs.writeFileSync(path.join(artifactsDir, 'report.md'), '# report\n', 'utf8');
    fs.writeFileSync(
      path.join(artifactsDir, 'findings.json'),
      '{"findings":[]}\n',
      'utf8',
    );
    fs.mkdirSync(path.join(artifactsDir, '01-taskflow-chromium'), {
      recursive: true,
    });
    fs.writeFileSync(
      path.join(artifactsDir, '01-taskflow-chromium', 'trace.zip'),
      'trace',
    );

    const bundle = stageCiArtifacts({
      artifactsDir,
      findingsMarkdownPath: findingsSrc,
      repoRoot: dir,
    });

    assert.equal(fs.existsSync(bundle.findingsCopyPath), true);
    assert.equal(
      fs.readFileSync(bundle.findingsCopyPath, 'utf8'),
      '# findings\n',
    );
    assert.equal(fs.existsSync(bundle.manifestPath), true);

    const manifest = JSON.parse(fs.readFileSync(bundle.manifestPath, 'utf8')) as {
      files: string[];
      coreFiles: string[];
    };
    assert.ok(manifest.files.includes('PLATFORM_FINDINGS.md'));
    assert.ok(manifest.files.includes('report.md'));
    assert.ok(manifest.files.includes('findings.json'));
    assert.ok(manifest.files.includes('01-taskflow-chromium/trace.zip'));
    assert.ok(manifest.coreFiles.includes('PLATFORM_FINDINGS.md'));
    assert.ok(
      CI_ARTIFACT_CORE_FILES.every((name) => typeof name === 'string'),
    );
    assert.ok(bundle.relativePaths.includes('upload-manifest.json'));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
