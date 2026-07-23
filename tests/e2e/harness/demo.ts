import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

export interface DemoHost {
  host: string;
  path: string;
  expect: number;
}

export interface DemoProject {
  id: string;
  title: string;
  epic: string;
  compose?: string;
  deploy: string;
  seed?: string;
  hosts: DemoHost[];
  baseURL: string;
  spec: string;
  services: string[];
  teardown: string;
}

export interface ScriptResult {
  command: string;
  code: number;
  stdout: string;
  stderr: string;
  durationMs: number;
}

export interface LifecycleResult {
  project: DemoProject;
  baseURL: string;
  steps: ScriptResult[];
}

export class DemoValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DemoValidationError';
  }
}

export class ScriptExecutionError extends Error {
  readonly result: ScriptResult;

  constructor(message: string, result: ScriptResult) {
    super(message);
    this.name = 'ScriptExecutionError';
    this.result = result;
  }
}

export interface DemoLifecycleOptions {
  /** Repository root used to resolve script paths (default: three levels above this file). */
  repoRoot?: string;
  timeoutMs?: number;
  env?: NodeJS.ProcessEnv;
  /** When true, skip teardown in run(). */
  keep?: boolean;
}

const DEFAULT_TIMEOUT_MS = 300_000;

const SCHEMA_PATH = path.join(__dirname, 'demo.schema.json');

type JsonSchema = {
  type?: string;
  required?: string[];
  additionalProperties?: boolean;
  properties?: Record<string, JsonSchema>;
  items?: JsonSchema;
  minItems?: number;
  minLength?: number;
  minimum?: number;
  maximum?: number;
};

let cachedSchema: JsonSchema | undefined;

function loadSchema(): JsonSchema {
  if (!cachedSchema) {
    cachedSchema = JSON.parse(fs.readFileSync(SCHEMA_PATH, 'utf8')) as JsonSchema;
  }
  return cachedSchema;
}

/**
 * Validate a demo.json path (or already-parsed object) against demo.schema.json.
 * Returns the typed DemoProject on success; throws DemoValidationError otherwise.
 */
export function validate(pathOrObject: string | unknown): DemoProject {
  const value =
    typeof pathOrObject === 'string' ? readJsonFile(pathOrObject) : pathOrObject;
  const errors: string[] = [];
  validateAgainstSchema(value, loadSchema(), '$', errors);
  if (errors.length > 0) {
    throw new DemoValidationError(
      `invalid demo.json:\n  - ${errors.join('\n  - ')}`,
    );
  }
  return value as DemoProject;
}

/** Load and validate a demo.json file. */
export function load(demoJsonPath: string): DemoProject {
  return validate(demoJsonPath);
}

export class DemoLifecycle {
  readonly project: DemoProject;
  readonly repoRoot: string;
  readonly timeoutMs: number;
  readonly env: NodeJS.ProcessEnv;
  readonly keep: boolean;
  private readonly results: ScriptResult[] = [];

  constructor(project: DemoProject, options: DemoLifecycleOptions = {}) {
    this.project = project;
    this.repoRoot =
      options.repoRoot ?? path.resolve(__dirname, '../../..');
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.env = { ...process.env, ...options.env };
    this.keep = options.keep ?? process.env.KEEP === '1';
  }

  get baseURL(): string {
    return this.project.baseURL;
  }

  get steps(): readonly ScriptResult[] {
    return this.results;
  }

  /** Product up: run deploy script (Ready is implied by successful exit; health waits are 50.03). */
  async up(): Promise<ScriptResult> {
    return this.execStep('deploy', this.project.deploy);
  }

  /** Idempotent seed (no-op when seed is omitted). */
  async seed(): Promise<ScriptResult | null> {
    if (!this.project.seed) {
      return null;
    }
    return this.execStep('seed', this.project.seed);
  }

  /** Tear down product resources. */
  async down(): Promise<ScriptResult> {
    return this.execStep('teardown', this.project.teardown);
  }

  /**
   * Run up → seed → (caller owns browser test) → down.
   * Does not invoke Playwright; exposes baseURL for the test step.
   */
  async run(
    testFn?: (ctx: { baseURL: string; project: DemoProject }) => Promise<void>,
  ): Promise<LifecycleResult> {
    try {
      await this.up();
      await this.seed();
      if (testFn) {
        await testFn({ baseURL: this.baseURL, project: this.project });
      }
    } finally {
      if (!this.keep) {
        await this.down();
      }
    }
    return {
      project: this.project,
      baseURL: this.baseURL,
      steps: [...this.results],
    };
  }

  private async execStep(name: string, commandLine: string): Promise<ScriptResult> {
    const result = await runCommandLine(commandLine, {
      cwd: this.repoRoot,
      timeoutMs: this.timeoutMs,
      env: this.env,
    });
    this.results.push(result);
    if (result.code !== 0) {
      const preview = [result.stderr, result.stdout]
        .map((s) => s.trim())
        .filter(Boolean)
        .join('\n')
        .slice(0, 2000);
      throw new ScriptExecutionError(
        `demo ${this.project.id} ${name} failed (exit ${result.code}): ${commandLine}` +
          (preview ? `\n${preview}` : ''),
        result,
      );
    }
    return result;
  }
}

function readJsonFile(filePath: string): unknown {
  const resolved = path.resolve(filePath);
  if (!fs.existsSync(resolved)) {
    throw new DemoValidationError(`demo.json not found: ${resolved}`);
  }
  try {
    return JSON.parse(fs.readFileSync(resolved, 'utf8')) as unknown;
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new DemoValidationError(`demo.json is not valid JSON (${resolved}): ${message}`);
  }
}

function validateAgainstSchema(
  value: unknown,
  schema: JsonSchema,
  pointer: string,
  errors: string[],
): void {
  if (schema.type === 'object') {
    if (!isPlainObject(value)) {
      errors.push(`${pointer}: expected object`);
      return;
    }
    for (const key of schema.required ?? []) {
      if (!(key in value)) {
        errors.push(`${pointer}: missing required property "${key}"`);
      }
    }
    if (schema.additionalProperties === false) {
      const allowed = new Set(Object.keys(schema.properties ?? {}));
      for (const key of Object.keys(value)) {
        if (!allowed.has(key)) {
          errors.push(`${pointer}: unexpected property "${key}"`);
        }
      }
    }
    for (const [key, propSchema] of Object.entries(schema.properties ?? {})) {
      if (key in value) {
        validateAgainstSchema(
          (value as Record<string, unknown>)[key],
          propSchema,
          `${pointer}.${key}`,
          errors,
        );
      }
    }
    return;
  }

  if (schema.type === 'array') {
    if (!Array.isArray(value)) {
      errors.push(`${pointer}: expected array`);
      return;
    }
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      errors.push(
        `${pointer}: expected at least ${schema.minItems} item(s), got ${value.length}`,
      );
    }
    if (schema.items) {
      value.forEach((item, index) => {
        validateAgainstSchema(item, schema.items as JsonSchema, `${pointer}[${index}]`, errors);
      });
    }
    return;
  }

  if (schema.type === 'string') {
    if (typeof value !== 'string') {
      errors.push(`${pointer}: expected string`);
      return;
    }
    if (schema.minLength !== undefined && value.length < schema.minLength) {
      errors.push(`${pointer}: string shorter than minLength ${schema.minLength}`);
    }
    return;
  }

  if (schema.type === 'integer') {
    if (typeof value !== 'number' || !Number.isInteger(value)) {
      errors.push(`${pointer}: expected integer`);
      return;
    }
    if (schema.minimum !== undefined && value < schema.minimum) {
      errors.push(`${pointer}: expected >= ${schema.minimum}`);
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      errors.push(`${pointer}: expected <= ${schema.maximum}`);
    }
  }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function splitCommand(commandLine: string): { command: string; args: string[] } {
  const parts = commandLine.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) {
    throw new DemoValidationError('command line is empty');
  }
  return { command: parts[0], args: parts.slice(1) };
}

export function runCommandLine(
  commandLine: string,
  opts: { cwd: string; timeoutMs: number; env: NodeJS.ProcessEnv },
): Promise<ScriptResult> {
  const { command, args } = splitCommand(commandLine);
  const resolved = path.isAbsolute(command)
    ? command
    : path.resolve(opts.cwd, command);

  return new Promise((resolve, reject) => {
    const started = Date.now();
    const child = spawn(resolved, args, {
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
          `command timed out after ${opts.timeoutMs}ms: ${commandLine}`,
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
        new Error(`failed to spawn "${resolved}": ${err.message}`),
      );
    });

    child.on('close', (code) => {
      settle(resolve, {
        command: commandLine,
        code: code ?? 1,
        stdout,
        stderr,
        durationMs: Date.now() - started,
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
