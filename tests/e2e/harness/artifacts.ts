import fs from 'node:fs';
import path from 'node:path';

/**
 * CI artifact staging for the platform E2E harness (56.04).
 *
 * When CI=1, the orchestrator stages a single upload bundle under
 * `tests/e2e/artifacts/` — traces/videos/screenshots (Playwright), reports,
 * findings.json, and a copy of PLATFORM_FINDINGS.md — plus a machine-readable
 * manifest for GitHub Actions `actions/upload-artifact`.
 */

function e2eRoot(): string {
  return path.resolve(__dirname, '..');
}

function defaultRepoRoot(): string {
  return path.resolve(e2eRoot(), '../..');
}

export function defaultArtifactsDir(): string {
  return path.join(e2eRoot(), 'artifacts');
}

/** Names always expected in the CI upload bundle when present. */
export const CI_ARTIFACT_CORE_FILES = [
  'report.md',
  'report.html',
  'findings.json',
  'orchestrator-result.json',
  'PLATFORM_FINDINGS.md',
  'upload-manifest.json',
] as const;

export interface StageCiArtifactsOptions {
  artifactsDir?: string;
  /** Source PLATFORM_FINDINGS.md (default: docs/demo-projects/PLATFORM_FINDINGS.md). */
  findingsMarkdownPath?: string;
  repoRoot?: string;
}

export interface CiArtifactBundle {
  artifactsDir: string;
  /** Absolute paths present and ready for upload. */
  stagedPaths: string[];
  /** Relative paths from artifactsDir (plus manifest). */
  relativePaths: string[];
  manifestPath: string;
  findingsCopyPath: string;
}

function listRelativeFiles(dir: string, base: string = dir): string[] {
  if (!fs.existsSync(dir)) return [];
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const abs = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...listRelativeFiles(abs, base));
    } else if (entry.isFile()) {
      out.push(path.relative(base, abs));
    }
  }
  return out.sort();
}

/**
 * Stage CI artifacts: copy PLATFORM_FINDINGS.md into artifacts/ and write
 * `upload-manifest.json` listing every file under the artifacts directory.
 */
export function stageCiArtifacts(
  options: StageCiArtifactsOptions = {},
): CiArtifactBundle {
  const repoRoot = options.repoRoot ?? defaultRepoRoot();
  const artifactsDir = options.artifactsDir ?? defaultArtifactsDir();
  const findingsMarkdownPath =
    options.findingsMarkdownPath ??
    path.join(repoRoot, 'docs/demo-projects/PLATFORM_FINDINGS.md');

  fs.mkdirSync(artifactsDir, { recursive: true });

  const findingsCopyPath = path.join(artifactsDir, 'PLATFORM_FINDINGS.md');
  if (fs.existsSync(findingsMarkdownPath)) {
    fs.copyFileSync(findingsMarkdownPath, findingsCopyPath);
  } else {
    fs.writeFileSync(
      findingsCopyPath,
      '# Platform findings\n\n_(source PLATFORM_FINDINGS.md missing at stage time)_\n',
      'utf8',
    );
  }

  const relativePaths = listRelativeFiles(artifactsDir).filter(
    (p) => p !== 'upload-manifest.json',
  );
  const stagedPaths = relativePaths.map((p) => path.join(artifactsDir, p));

  const manifest = {
    generatedAt: new Date().toISOString(),
    artifactsDir: path.relative(repoRoot, artifactsDir) || artifactsDir,
    coreFiles: CI_ARTIFACT_CORE_FILES.filter((name) =>
      relativePaths.includes(name),
    ),
    files: relativePaths,
    notes:
      'Upload tests/e2e/artifacts/ (this directory) from CI. Includes traces, videos, ' +
      'screenshots, report.md/html, findings.json, orchestrator-result.json, and PLATFORM_FINDINGS.md.',
  };

  const manifestPath = path.join(artifactsDir, 'upload-manifest.json');
  fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, 'utf8');

  const allRelative = [...relativePaths, 'upload-manifest.json'];
  return {
    artifactsDir,
    stagedPaths: [...stagedPaths, manifestPath],
    relativePaths: allRelative,
    manifestPath,
    findingsCopyPath,
  };
}

/** True when process/env indicates a CI run that should stage the upload bundle. */
export function shouldStageCiArtifacts(
  source: NodeJS.ProcessEnv | Partial<NodeJS.ProcessEnv> = process.env,
): boolean {
  return source.CI === '1' || source.CI === 'true';
}
