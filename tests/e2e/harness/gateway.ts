import http from 'node:http';
import type { AddressInfo } from 'node:net';
import type { DemoHost } from './demo';

/**
 * Gateway host-routing helpers for *.localhost product URLs.
 *
 * curl-style preflights hit http://127.0.0.1:4000 with an explicit Host header.
 * Playwright/browser requests use http://{host}:4000 which sends Host with :4000;
 * forge-gateway strips the port in routes.Match (normalizeHost) — confirmed in 50.03.
 */

export const DEFAULT_GATEWAY_ORIGIN = 'http://127.0.0.1:4000';
export const DEFAULT_GATEWAY_PORT = 4000;

export interface GatewayRequestOptions {
  gatewayOrigin?: string;
  /** When true, send Host as `{host}:{gatewayPort}` (browser-like). Default false (curl-style). */
  includePortInHost?: boolean;
  gatewayPort?: number;
  method?: string;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

export interface GatewayResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
  hostHeader: string;
  url: string;
}

export class HostPreflightError extends Error {
  readonly failures: HostPreflightFailure[];

  constructor(failures: HostPreflightFailure[]) {
    const detail = failures
      .map(
        (f) =>
          `  - ${f.host}${f.path}: expected ${f.expect}, got ${f.status}` +
          (f.detail ? ` (${f.detail})` : ''),
      )
      .join('\n');
    super(`gateway host preflight failed:\n${detail}`);
    this.name = 'HostPreflightError';
    this.failures = failures;
  }
}

export interface HostPreflightFailure {
  host: string;
  path: string;
  expect: number;
  status: number;
  detail?: string;
}

export interface HostPortMatchResult {
  /** True when Host with and without :port yield the same status. */
  stripsPort: boolean;
  withoutPort: GatewayResponse;
  withPort: GatewayResponse;
  /** Platform finding stub when stripsPort is false (50.04 writes). */
  finding?: {
    severity: 'blocker';
    service: 'forge-gateway';
    demo: 'harness';
    title: string;
    tested: string;
    expected: string;
    actual: string;
    repro: string[];
  };
}

export interface HostRewriteProxy {
  /** Local origin Playwright should use, e.g. http://127.0.0.1:54321 */
  origin: string;
  port: number;
  close: () => Promise<void>;
}

/** Compute the product baseURL browsers open (RFC 6761 *.localhost → loopback). */
export function productBaseURL(
  host: string,
  gatewayPort: number = DEFAULT_GATEWAY_PORT,
): string {
  const hostname = stripHostPort(host);
  return `http://${hostname}:${gatewayPort}`;
}

/** Strip an optional :port from a Host value (IPv4 / hostname). */
export function stripHostPort(host: string): string {
  const trimmed = host.trim();
  if (!trimmed) return '';
  // Bracketed IPv6 — leave as-is if no trailing :port we can parse simply.
  if (trimmed.startsWith('[')) {
    const end = trimmed.indexOf(']');
    if (end !== -1) return trimmed.slice(0, end + 1).toLowerCase();
  }
  const idx = trimmed.lastIndexOf(':');
  if (idx === -1) return trimmed.toLowerCase();
  const maybePort = trimmed.slice(idx + 1);
  if (/^\d+$/.test(maybePort)) {
    return trimmed.slice(0, idx).toLowerCase();
  }
  return trimmed.toLowerCase();
}

/**
 * Hit the Gateway with an explicit Host header (curl -H 'Host: ...' style).
 * Uses node:http so Host is not stripped (undici fetch forbids setting Host).
 */
export function fetchWithHost(
  host: string,
  path: string = '/',
  options: GatewayRequestOptions = {},
): Promise<GatewayResponse> {
  const gatewayOrigin = options.gatewayOrigin ?? DEFAULT_GATEWAY_ORIGIN;
  const gatewayPort = options.gatewayPort ?? DEFAULT_GATEWAY_PORT;
  const hostname = stripHostPort(host);
  const hostHeader = options.includePortInHost
    ? `${hostname}:${gatewayPort}`
    : hostname;
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  const target = new URL(normalizedPath, gatewayOrigin);
  const url = target.toString();

  return new Promise((resolve, reject) => {
    const req = http.request(
      {
        protocol: target.protocol,
        hostname: target.hostname,
        port: target.port || undefined,
        path: target.pathname + target.search,
        method: options.method ?? 'GET',
        headers: {
          ...(options.headers ?? {}),
          host: hostHeader,
        },
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (c: Buffer) => chunks.push(c));
        res.on('end', () => {
          const headers: Record<string, string> = {};
          for (const [key, value] of Object.entries(res.headers)) {
            if (typeof value === 'string') headers[key] = value;
            else if (Array.isArray(value)) headers[key] = value.join(', ');
          }
          resolve({
            status: res.statusCode ?? 0,
            headers,
            body: Buffer.concat(chunks).toString('utf8'),
            hostHeader,
            url,
          });
        });
      },
    );

    const onAbort = () => {
      req.destroy(new Error('aborted'));
    };
    if (options.signal) {
      if (options.signal.aborted) {
        onAbort();
        return;
      }
      options.signal.addEventListener('abort', onAbort, { once: true });
    } else {
      req.setTimeout(10_000, () => {
        req.destroy(new Error(`timeout fetching ${url}`));
      });
    }

    req.on('error', reject);
    req.end();
  });
}

/**
 * Verify each demo.json hosts[] entry returns the expected status via Gateway.
 */
export async function preflightHosts(
  hosts: DemoHost[],
  options: GatewayRequestOptions = {},
): Promise<GatewayResponse[]> {
  const failures: HostPreflightFailure[] = [];
  const results: GatewayResponse[] = [];

  for (const entry of hosts) {
    try {
      const res = await fetchWithHost(entry.host, entry.path, options);
      results.push(res);
      if (res.status !== entry.expect) {
        failures.push({
          host: entry.host,
          path: entry.path,
          expect: entry.expect,
          status: res.status,
          detail: res.body.slice(0, 200),
        });
      }
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err);
      failures.push({
        host: entry.host,
        path: entry.path,
        expect: entry.expect,
        status: 0,
        detail,
      });
    }
  }

  if (failures.length > 0) {
    throw new HostPreflightError(failures);
  }
  return results;
}

/**
 * Confirm Gateway matches Host ignoring :4000 (browser sends port-suffixed Host).
 * When it does not, returns a blocker finding stub and callers should use
 * {@link startHostRewriteProxy}.
 */
export async function verifyHostPortMatching(
  host: string,
  path: string = '/',
  options: GatewayRequestOptions = {},
): Promise<HostPortMatchResult> {
  const withoutPort = await fetchWithHost(host, path, {
    ...options,
    includePortInHost: false,
  });
  const withPort = await fetchWithHost(host, path, {
    ...options,
    includePortInHost: true,
  });

  const stripsPort = withoutPort.status === withPort.status;
  if (stripsPort) {
    return { stripsPort: true, withoutPort, withPort };
  }

  return {
    stripsPort: false,
    withoutPort,
    withPort,
    finding: {
      severity: 'blocker',
      service: 'forge-gateway',
      demo: 'harness',
      title: 'Gateway does not match Host when port-suffixed (:4000)',
      tested: `GET ${path} with Host: ${stripHostPort(host)} vs Host: ${stripHostPort(host)}:4000`,
      expected: 'identical status for Host with and without :4000',
      actual: `withoutPort=${withoutPort.status}, withPort=${withPort.status}`,
      repro: [
        `curl -s -o /dev/null -w '%{http_code}' -H 'Host: ${stripHostPort(host)}' http://127.0.0.1:4000${path}`,
        `curl -s -o /dev/null -w '%{http_code}' -H 'Host: ${stripHostPort(host)}:4000' http://127.0.0.1:4000${path}`,
      ],
    },
  };
}

/**
 * Harness-only fallback: tiny reverse proxy that rewrites Host to strip :port
 * before forwarding to the real Gateway. Used only when verifyHostPortMatching
 * reports stripsPort=false — never ships inside demo products.
 */
export async function startHostRewriteProxy(
  options: {
    gatewayOrigin?: string;
    listenHost?: string;
    listenPort?: number;
  } = {},
): Promise<HostRewriteProxy> {
  const gatewayOrigin = options.gatewayOrigin ?? DEFAULT_GATEWAY_ORIGIN;
  const listenHost = options.listenHost ?? '127.0.0.1';

  const server = http.createServer((req, res) => {
    const incomingHost = req.headers.host ?? '';
    const rewritten = stripHostPort(incomingHost);
    const targetUrl = new URL(req.url ?? '/', gatewayOrigin);

    const headers: http.OutgoingHttpHeaders = { ...req.headers, host: rewritten };
    delete headers['content-length'];

    const upstream = http.request(
      {
        protocol: targetUrl.protocol,
        hostname: targetUrl.hostname,
        port: targetUrl.port || undefined,
        path: targetUrl.pathname + targetUrl.search,
        method: req.method,
        headers,
      },
      (upRes) => {
        res.writeHead(upRes.statusCode ?? 502, upRes.headers);
        upRes.pipe(res);
      },
    );
    upstream.on('error', (err) => {
      res.statusCode = 502;
      res.end(`host-rewrite proxy upstream error: ${err.message}`);
    });
    req.pipe(upstream);
  });

  await new Promise<void>((resolve, reject) => {
    server.once('error', reject);
    server.listen(options.listenPort ?? 0, listenHost, () => resolve());
  });

  const addr = server.address() as AddressInfo;
  return {
    origin: `http://${listenHost}:${addr.port}`,
    port: addr.port,
    close: () =>
      new Promise((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
      }),
  };
}
