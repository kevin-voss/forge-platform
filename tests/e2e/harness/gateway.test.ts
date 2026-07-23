import assert from 'node:assert/strict';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import test from 'node:test';

import type { DemoHost } from './demo';
import {
  HostPreflightError,
  fetchWithHost,
  preflightHosts,
  productBaseURL,
  startHostRewriteProxy,
  stripHostPort,
  verifyHostPortMatching,
} from './gateway';

function startMockGateway(
  handler: (req: http.IncomingMessage, res: http.ServerResponse) => void,
): Promise<{ origin: string; close: () => Promise<void> }> {
  const server = http.createServer(handler);
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address() as AddressInfo;
      resolve({
        origin: `http://127.0.0.1:${port}`,
        close: () =>
          new Promise((res, rej) =>
            server.close((err) => (err ? rej(err) : res())),
          ),
      });
    });
  });
}

test('productBaseURL and stripHostPort shape browser URLs', () => {
  assert.equal(
    productBaseURL('app.taskflow.localhost'),
    'http://app.taskflow.localhost:4000',
  );
  assert.equal(stripHostPort('app.taskflow.localhost:4000'), 'app.taskflow.localhost');
  assert.equal(stripHostPort('App.TaskFlow.Localhost'), 'app.taskflow.localhost');
});

test('fetchWithHost sends explicit Host header (curl-style)', async () => {
  let sawHost = '';
  const mock = await startMockGateway((req, res) => {
    sawHost = req.headers.host ?? '';
    res.writeHead(200, { 'content-type': 'text/plain' });
    res.end('ok');
  });
  try {
    const res = await fetchWithHost('grafana.localhost', '/api/health', {
      gatewayOrigin: mock.origin,
    });
    assert.equal(res.status, 200);
    assert.equal(res.hostHeader, 'grafana.localhost');
    assert.equal(sawHost, 'grafana.localhost');
    assert.equal(res.body, 'ok');
  } finally {
    await mock.close();
  }
});

test('host preflight against a known service returns expected status', async () => {
  // Mock Gateway that routes grafana.localhost → 200 like a demo host.
  const mock = await startMockGateway((req, res) => {
    const host = (req.headers.host ?? '').split(':')[0].toLowerCase();
    if (host === 'grafana.localhost' && req.url === '/api/health') {
      res.writeHead(200);
      res.end('{"database":"ok"}');
      return;
    }
    res.writeHead(404);
    res.end('not found');
  });

  try {
    const hosts: DemoHost[] = [
      { host: 'grafana.localhost', path: '/api/health', expect: 200 },
    ];
    const results = await preflightHosts(hosts, { gatewayOrigin: mock.origin });
    assert.equal(results.length, 1);
    assert.equal(results[0].status, 200);
  } finally {
    await mock.close();
  }
});

test('preflightHosts fails clearly on wrong status', async () => {
  const mock = await startMockGateway((_req, res) => {
    res.writeHead(503);
    res.end('down');
  });
  try {
    await assert.rejects(
      () =>
        preflightHosts(
          [{ host: 'app.fixture.localhost', path: '/', expect: 200 }],
          { gatewayOrigin: mock.origin },
        ),
      (err: unknown) => {
        assert.ok(err instanceof HostPreflightError);
        assert.equal(err.failures[0].status, 503);
        assert.equal(err.failures[0].expect, 200);
        return true;
      },
    );
  } finally {
    await mock.close();
  }
});

test('verifyHostPortMatching detects port-stripping Gateway', async () => {
  // Gateway-like: match on hostname ignoring :port.
  const mock = await startMockGateway((req, res) => {
    const raw = req.headers.host ?? '';
    const host = raw.includes(':') ? raw.slice(0, raw.lastIndexOf(':')) : raw;
    if (host.toLowerCase() === 'go.demo.localhost') {
      res.writeHead(200);
      res.end('matched');
      return;
    }
    res.writeHead(404);
    res.end('miss');
  });

  try {
    const result = await verifyHostPortMatching('go.demo.localhost', '/', {
      gatewayOrigin: mock.origin,
      gatewayPort: 4000,
    });
    assert.equal(result.stripsPort, true);
    assert.equal(result.withoutPort.status, 200);
    assert.equal(result.withPort.status, 200);
    assert.equal(result.withPort.hostHeader, 'go.demo.localhost:4000');
    assert.equal(result.finding, undefined);
  } finally {
    await mock.close();
  }
});

test('verifyHostPortMatching records finding when port-suffixed Host 404s', async () => {
  // Broken Gateway: only exact Host without port matches.
  const mock = await startMockGateway((req, res) => {
    if (req.headers.host === 'go.demo.localhost') {
      res.writeHead(200);
      res.end('ok');
      return;
    }
    res.writeHead(404);
    res.end('miss');
  });

  try {
    const result = await verifyHostPortMatching('go.demo.localhost', '/', {
      gatewayOrigin: mock.origin,
      gatewayPort: 4000,
    });
    assert.equal(result.stripsPort, false);
    assert.ok(result.finding);
    assert.equal(result.finding?.service, 'forge-gateway');
    assert.equal(result.finding?.severity, 'blocker');
  } finally {
    await mock.close();
  }
});

test('host-rewrite proxy strips :port before forwarding', async () => {
  let upstreamHost = '';
  const upstream = await startMockGateway((req, res) => {
    upstreamHost = req.headers.host ?? '';
    res.writeHead(200);
    res.end('proxied');
  });
  const proxy = await startHostRewriteProxy({
    gatewayOrigin: upstream.origin,
  });

  try {
    const body = await new Promise<string>((resolve, reject) => {
      const req = http.request(
        `${proxy.origin}/hello`,
        { headers: { host: 'app.taskflow.localhost:4000' } },
        (res) => {
          const chunks: Buffer[] = [];
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
        },
      );
      req.on('error', reject);
      req.end();
    });
    assert.equal(body, 'proxied');
    assert.equal(upstreamHost, 'app.taskflow.localhost');
  } finally {
    await proxy.close();
    await upstream.close();
  }
});
