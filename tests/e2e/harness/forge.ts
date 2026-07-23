import { spawn } from 'node:child_process';
import path from 'node:path';

export interface ForgeResult {
  code: number;
  stdout: string;
  stderr: string;
  args: string[];
}

export class ForgeError extends Error {
  readonly result: ForgeResult;

  constructor(message: string, result: ForgeResult) {
    super(message);
    this.name = 'ForgeError';
    this.result = result;
  }
}

export interface ForgeOptions {
  /** Path to the `forge` binary (default: FORGE_BIN or `forge` on PATH). */
  bin?: string;
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  timeoutMs?: number;
  /** When false, non-zero exits return ForgeResult instead of throwing. Default true. */
  throwOnError?: boolean;
}

const DEFAULT_TIMEOUT_MS = 120_000;

export class Forge {
  readonly bin: string;
  readonly cwd: string;
  readonly env: NodeJS.ProcessEnv;
  readonly timeoutMs: number;
  readonly throwOnError: boolean;

  constructor(options: ForgeOptions = {}) {
    this.bin = options.bin ?? process.env.FORGE_BIN ?? 'forge';
    this.cwd = options.cwd ?? process.cwd();
    this.env = { ...process.env, ...options.env };
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.throwOnError = options.throwOnError ?? true;
  }

  build(args: string[] = []): Promise<ForgeResult> {
    return this.run(['build', ...args]);
  }

  apply(args: string[]): Promise<ForgeResult> {
    return this.run(['apply', ...args]);
  }

  get(args: string[]): Promise<ForgeResult> {
    return this.run(['get', ...args]);
  }

  wait(args: string[]): Promise<ForgeResult> {
    return this.run(['wait', ...args]);
  }

  logs(args: string[] = []): Promise<ForgeResult> {
    return this.run(['logs', ...args]);
  }

  async run(args: string[]): Promise<ForgeResult> {
    const result = await spawnCapture(this.bin, args, {
      cwd: this.cwd,
      env: this.env,
      timeoutMs: this.timeoutMs,
    });

    if (result.code !== 0 && this.throwOnError) {
      const preview = [result.stderr, result.stdout]
        .map((s) => s.trim())
        .filter(Boolean)
        .join('\n')
        .slice(0, 2000);
      throw new ForgeError(
        `forge ${args.join(' ')} exited ${result.code}` +
          (preview ? `:\n${preview}` : ''),
        result,
      );
    }

    return result;
  }
}

function spawnCapture(
  command: string,
  args: string[],
  opts: { cwd: string; env: NodeJS.ProcessEnv; timeoutMs: number },
): Promise<ForgeResult> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: opts.cwd,
      env: opts.env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    let stdout = '';
    let stderr = '';
    let settled = false;

    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      settle(
        reject,
        new Error(
          `forge ${args.join(' ')} timed out after ${opts.timeoutMs}ms` +
            ` (bin=${path.basename(command)})`,
        ),
      );
    }, opts.timeoutMs);

    child.stdout.on('data', (chunk: Buffer | string) => {
      stdout += chunk.toString();
    });
    child.stderr.on('data', (chunk: Buffer | string) => {
      stderr += chunk.toString();
    });

    child.on('error', (err) => {
      settle(
        reject,
        new Error(
          `failed to spawn forge (${command}): ${err.message}`,
        ),
      );
    });

    child.on('close', (code) => {
      settle(resolve, {
        code: code ?? 1,
        stdout,
        stderr,
        args,
      });
    });

    function settle<T>(fn: (value: T) => void, value: T): void {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      fn(value);
    }
  });
}
