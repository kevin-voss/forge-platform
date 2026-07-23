import { spawn } from 'node:child_process';
import net from 'node:net';
import path from 'node:path';

/**
 * Platform preflight for the E2E harness.
 *
 * Brings up local Compose infra via `make dev` when needed, then waits for the
 * same health endpoints `make wait` covers (via scripts/wait-for-service.sh).
 * Failures surface as a single blocker stub (findings writer lands in 50.04).
 */

export interface HealthCheck {
  /** Stable id used in error messages (e.g. "vault", "grafana"). */
  name: string;
  /** HTTP URL to poll, or omit when using tcpPort. */
  url?: string;
  /** TCP port on 127.0.0.1 (PostgreSQL). */
  tcpPort?: number;
}

export interface PlatformPreflightOptions {
  repoRoot?: string;
  /** Per-check timeout (default 90s, matching Makefile wait). */
  timeoutSeconds?: number;
  /** Overall budget for ensure+wait (default 10 minutes). */
  overallTimeoutMs?: number;
  /** Skip `make dev` and only wait (useful in tests). */
  skipEnsure?: boolean;
  /** Override the health checklist (default: make wait ports). */
  checks?: HealthCheck[];
  /** Inject wait implementation (tests). */
  waitForUrl?: (url: string, timeoutSeconds: number) => Promise<void>;
  /** Inject make-dev (tests). */
  runMakeDev?: () => Promise<void>;
  /** Inject "is already up?" probe (tests). */
  isUp?: (checks: HealthCheck[]) => Promise<boolean>;
}

/** Stub blocker finding — 50.04 owns the real writer. */
export interface PreflightBlockerFinding {
  severity: 'blocker';
  service: 'platform';
  demo: 'harness';
  title: string;
  tested: string;
  expected: string;
  actual: string;
  repro: string[];
}

export class PreflightError extends Error {
  readonly finding: PreflightBlockerFinding;

  constructor(finding: PreflightBlockerFinding) {
    super(
      `platform preflight blocked: ${finding.title}\n` +
        `  expected: ${finding.expected}\n` +
        `  actual:   ${finding.actual}`,
    );
    this.name = 'PreflightError';
    this.finding = finding;
  }
}

/** Health endpoints from root `Makefile` `wait` target. */
export const MAKE_WAIT_CHECKS: HealthCheck[] = [
  { name: 'vault', url: 'http://127.0.0.1:5003/healthz' },
  { name: 'registry', url: 'http://127.0.0.1:5000/v2/' },
  { name: 'otel-collector', url: 'http://127.0.0.1:13133/' },
  { name: 'prometheus', url: 'http://127.0.0.1:3001/-/healthy' },
  { name: 'tempo', url: 'http://127.0.0.1:3002/ready' },
  { name: 'loki', url: 'http://127.0.0.1:3003/ready' },
  { name: 'grafana', url: 'http://127.0.0.1:3000/api/health' },
  { name: 'postgres', tcpPort: 5001 },
];

const DEFAULT_TIMEOUT_SECONDS = 90;
const DEFAULT_OVERALL_MS = 600_000;

function defaultRepoRoot(): string {
  return path.resolve(__dirname, '../../..');
}

/**
 * Bring platform infra up if needed, then wait until every make-wait check is healthy.
 * Throws PreflightError with a single blocker finding on failure.
 */
export async function preflight(
  options: PlatformPreflightOptions = {},
): Promise<void> {
  const checks = options.checks ?? MAKE_WAIT_CHECKS;
  const timeoutSeconds = options.timeoutSeconds ?? DEFAULT_TIMEOUT_SECONDS;
  const overallTimeoutMs = options.overallTimeoutMs ?? DEFAULT_OVERALL_MS;
  const repoRoot = options.repoRoot ?? defaultRepoRoot();
  const started = Date.now();

  const isUp = options.isUp ?? defaultIsUp;
  const runMakeDev = options.runMakeDev ?? (() => runMakeDevDefault(repoRoot));
  const waitForUrl = options.waitForUrl ?? waitForServiceScript(repoRoot);

  try {
    const alreadyUp = await isUp(checks);
    if (!alreadyUp && !options.skipEnsure) {
      await withOverallBudget(overallTimeoutMs, started, runMakeDev);
    }

    for (const check of checks) {
      const remaining = overallTimeoutMs - (Date.now() - started);
      if (remaining <= 0) {
        throw new Error(`overall preflight timeout after ${overallTimeoutMs}ms`);
      }
      const checkTimeout = Math.min(
        timeoutSeconds,
        Math.max(1, Math.ceil(remaining / 1000)),
      );

      if (check.url) {
        await waitForUrl(check.url, checkTimeout);
      } else if (check.tcpPort !== undefined) {
        await waitForTcp(check.tcpPort, checkTimeout, check.name);
      } else {
        throw new Error(`health check "${check.name}" has neither url nor tcpPort`);
      }
    }
  } catch (err) {
    const actual = err instanceof Error ? err.message : String(err);
    throw new PreflightError({
      severity: 'blocker',
      service: 'platform',
      demo: 'harness',
      title: 'Platform preflight failed — local infra not healthy',
      tested: 'make dev (if needed) + wait for make wait health endpoints',
      expected: `all of: ${checks.map((c) => c.name).join(', ')} healthy within timeout`,
      actual,
      repro: ['make dev', 'make status', 'cd tests/e2e && node -e "require(\'./harness/platform\').preflight()"'],
    });
  }
}

async function withOverallBudget(
  overallTimeoutMs: number,
  started: number,
  fn: () => Promise<void>,
): Promise<void> {
  const remaining = overallTimeoutMs - (Date.now() - started);
  if (remaining <= 0) {
    throw new Error(`overall preflight timeout after ${overallTimeoutMs}ms`);
  }
  let timer: NodeJS.Timeout | undefined;
  try {
    await Promise.race([
      fn(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          reject(new Error(`overall preflight timeout after ${overallTimeoutMs}ms`));
        }, remaining);
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

async function defaultIsUp(checks: HealthCheck[]): Promise<boolean> {
  for (const check of checks) {
    if (check.url) {
      try {
        const res = await fetch(check.url, {
          signal: AbortSignal.timeout(2000),
        });
        if (!res.ok) return false;
      } catch {
        return false;
      }
    } else if (check.tcpPort !== undefined) {
      const open = await tcpOpen(check.tcpPort, 2000);
      if (!open) return false;
    }
  }
  return true;
}

function waitForServiceScript(
  repoRoot: string,
): (url: string, timeoutSeconds: number) => Promise<void> {
  const script = path.join(repoRoot, 'scripts/wait-for-service.sh');
  return (url, timeoutSeconds) =>
    new Promise((resolve, reject) => {
      const child = spawn(script, [url, String(timeoutSeconds)], {
        cwd: repoRoot,
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      let stdout = '';
      let stderr = '';
      child.stdout.on('data', (c: Buffer | string) => {
        stdout += c.toString();
      });
      child.stderr.on('data', (c: Buffer | string) => {
        stderr += c.toString();
      });
      child.on('error', (err) => reject(err));
      child.on('close', (code) => {
        if (code === 0) {
          resolve();
          return;
        }
        reject(
          new Error(
            `wait-for-service.sh failed for ${url} (exit ${code}): ${
              stderr.trim() || stdout.trim() || 'no output'
            }`,
          ),
        );
      });
    });
}

function runMakeDevDefault(repoRoot: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn('make', ['dev'], {
      cwd: repoRoot,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: process.env,
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (c: Buffer | string) => {
      stdout += c.toString();
    });
    child.stderr.on('data', (c: Buffer | string) => {
      stderr += c.toString();
    });
    child.on('error', (err) => reject(err));
    child.on('close', (code) => {
      if (code === 0) {
        resolve();
        return;
      }
      reject(
        new Error(
          `make dev exited ${code}: ${stderr.trim() || stdout.trim() || 'no output'}`,
        ),
      );
    });
  });
}

function waitForTcp(
  port: number,
  timeoutSeconds: number,
  name: string,
): Promise<void> {
  const deadline = Date.now() + timeoutSeconds * 1000;
  return new Promise((resolve, reject) => {
    const attempt = () => {
      tcpOpen(port, 2000).then((ok) => {
        if (ok) {
          resolve();
          return;
        }
        if (Date.now() >= deadline) {
          reject(new Error(`Timed out waiting for ${name} on 127.0.0.1:${port}`));
          return;
        }
        setTimeout(attempt, 1000);
      });
    };
    attempt();
  });
}

function tcpOpen(port: number, timeoutMs: number): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = net.connect({ host: '127.0.0.1', port });
    const done = (ok: boolean) => {
      socket.removeAllListeners();
      socket.destroy();
      resolve(ok);
    };
    socket.setTimeout(timeoutMs);
    socket.once('connect', () => done(true));
    socket.once('timeout', () => done(false));
    socket.once('error', () => done(false));
  });
}
