import { afterEach, describe, expect, it, vi } from 'vitest';
import { createDemoInventory } from './demo';
import { createConnector, getSetupStatus, loadContextExport, loadInventory, login } from './api';

afterEach(() => {
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

describe('loadInventory', () => {
  it('uses an isolated designed demo inventory when the API is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
    const inventory = await loadInventory();
    const other = createDemoInventory();

    inventory.services[0].name = 'changed locally';
    expect(inventory.source).toBe('demo');
    expect(inventory.error).toContain('offline');
    expect(other.services[0].name).toBe('immich-server');
    expect(inventory.services).not.toBe(other.services);
    expect(inventory.services[0]).not.toBe(other.services[0]);
  });

  it('preserves authoritative joined API fields and fetches expiry directly', async () => {
    const payloads: Record<string, unknown[]> = {
      services: [{ id: 8, name: 'actual-service', host_id: 3, host: 'joined-host', state: 'running', stack: 'actual-stack', image: 'repo/image', tag: 'v1', last_seen: 'now', ports: '9443 → 443/tcp', route: 'actual.lab.example' }],
      hosts: [{ id: 3, name: 'unrelated-host-record', kind: 'docker', address: '10.0.0.3', os: 'Linux', arch: 'amd64', state: 'active' }],
      ports: [{ id: 1, service_id: 8, service: 'joined-service', host_id: 3, host: 'joined-port-host', number: 9443, protocol: 'tcp', published: true, host_ip: '0.0.0.0', container_port: 443, source: 'docker' }],
      routes: [{ id: 4, proxy_id: 2, proxy: 'joined-proxy', domain: 'actual.lab.example', path_prefix: '/', upstream_host: 'actual-service', upstream_port: 443, resolved_service_id: 8, service: 'joined-route-service', resolve_confidence: 'high', tls: true, status: 'resolved', state: 'active', cert_expires_at: null }],
      changes: [],
      expiry: [{ entity_type: 'cert', id: 5, name: 'actual.lab.example', kind: 'TLS certificate', type: 'TLS certificate', authority: 'Let’s Encrypt', expires_at: '2026-08-01T00:00:00Z', expires: '2026-08-01T00:00:00Z', days_remaining: 16, days: 16, status: 'expiring', checked_at: '2026-07-16T00:00:00Z', source: 'cert', state: 'active' }],
      connectors: []
    };
    const requested: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const key = String(input).match(/\/api\/([^?]+)/)?.[1] ?? '';
      requested.push(key);
      return new Response(JSON.stringify({ items: payloads[key], total: payloads[key]?.length ?? 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));

    const inventory = await loadInventory({ demoOnEmpty: false });

    expect(inventory.source).toBe('api');
    expect(inventory.services[0]).toMatchObject({ host: 'joined-host', ports: '9443 → 443/tcp', route: 'actual.lab.example' });
    expect(inventory.ports[0]).toMatchObject({ service: 'joined-service', host: 'joined-port-host', published: true });
    expect(inventory.routes[0]).toMatchObject({ proxy: 'joined-proxy', service: 'joined-route-service', tls: true });
    expect(inventory.expiries[0]).toMatchObject({ days_remaining: 16, status: 'expiring' });
    expect(requested).toContain('expiry');
    expect(requested).not.toContain('certs');
    expect(requested).not.toContain('domains');
  });

  it('uses demo data for a successful empty development response', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ items: [], total: 0 }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    })));
    const inventory = await loadInventory({ demoOnEmpty: true });
    expect(inventory.source).toBe('demo');
    expect(inventory.services[0].name).toBe('immich-server');
  });

  it('keeps a successful empty API response empty outside the development preview', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ items: [], total: 0 }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' }
    })));
    const inventory = await loadInventory({ demoOnEmpty: false });
    expect(inventory.source).toBe('api');
    expect(inventory.services).toEqual([]);
    expect(inventory.hosts).toEqual([]);
    expect(inventory.expiries).toEqual([]);
  });

  it('preserves real inventory when connectors are forbidden by a share token', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const resource = String(input).match(/\/api\/([^?]+)/)?.[1];
      if (resource === 'connectors') return new Response('share token scope does not allow this resource', { status: 403 });
      const items = resource === 'services' ? [{ id: 7, name: 'shared-service' }] : [];
      return new Response(JSON.stringify({ items, total: items.length }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));

    const inventory = await loadInventory({ demoOnEmpty: false });

    expect(inventory.source).toBe('api');
    expect(inventory.readOnly).toBe(true);
    expect(inventory.services).toEqual([{ id: 7, name: 'shared-service' }]);
    expect(inventory.issues).toEqual([]);
  });

  it('never substitutes demo records for an empty shared inventory', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const resource = String(input).match(/\/api\/([^?]+)/)?.[1];
      return resource === 'connectors'
        ? new Response('share token scope does not allow this resource', { status: 403 })
        : new Response(JSON.stringify({ items: [], total: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));

    const inventory = await loadInventory({ demoOnEmpty: true });

    expect(inventory.source).toBe('api');
    expect(inventory.readOnly).toBe(true);
    expect(inventory.services).toEqual([]);
  });

  it('does not hide authentication failures behind development demo data', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('sign in required', { status: 401 })));

    const inventory = await loadInventory({ demoOnEmpty: true });

    expect(inventory.source).toBe('api');
    expect(inventory.issues).toHaveLength(7);
    expect(inventory.issues.every((issue) => issue.kind === 'unauthorized')).toBe(true);
  });

  it('keeps successful resources when one inventory API fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const resource = String(input).match(/\/api\/([^?]+)/)?.[1];
      if (resource === 'routes') return new Response('route lookup failed', { status: 503 });
      const items = resource === 'hosts' ? [{ id: 3, name: 'real-host' }] : [];
      return new Response(JSON.stringify({ items, total: items.length }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));

    const inventory = await loadInventory({ demoOnEmpty: false });

    expect(inventory.source).toBe('api');
    expect(inventory.hosts).toEqual([{ id: 3, name: 'real-host' }]);
    expect(inventory.routes).toEqual([]);
    expect(inventory.issues).toEqual([expect.objectContaining({ resource: 'routes', status: 503, kind: 'server' })]);
  });

  it('reads setup state and keeps the CSRF token from login', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ configured: true, auth_disabled: false }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ csrf: 'local-csrf' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    expect(await getSetupStatus()).toEqual({ configured: true, auth_disabled: false });
    await login('correct horse battery staple');
    expect(sessionStorage.getItem('homedex-csrf')).toBe('local-csrf');
  });
});

describe('createConnector', () => {
  it('returns a persisted connector from a failed first-scan response', async () => {
    const mutation = { connector: { id: 17, name: 'Local Docker' }, scan_run_id: 31, changes: 0, scan_error: 'socket unavailable' };
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(mutation), { status: 502, headers: { 'Content-Type': 'application/json' } })));

    await expect(createConnector({ kind: 'docker', name: 'Local Docker', config: { endpoint: 'unix:///var/run/docker.sock' } })).resolves.toEqual(mutation);
  });
});

describe('loadContextExport', () => {
  it('derives the receipt from the exact private-safe backend response', async () => {
    const markdown = `# Homedex lab context

Schema: \`homedex.inventory.v1\`

## Hosts

| Name | Kind |
|---|---|
| nas | docker |

## Services

| Host | Service |
|---|---|
| nas | photos |
| nas | database |

## Ports

| Host | Service |
|---|---|
| nas | photos |

## Routes

| Domain | Service |
|---|---|
| photos.lab | photos |

## Expiry

| Name | Type |
|---|---|
| photos.lab | TLS |

## Truncation report

- services: 0 omitted
`;
    const digestBytes = Uint8Array.from({ length: 32 }, (_, index) => index);
    const digest = vi.fn().mockResolvedValue(digestBytes.buffer);
    vi.stubGlobal('crypto', { subtle: { digest } });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(markdown, {
      status: 200,
      headers: {
        'Content-Type': 'text/markdown',
        'Content-Disposition': 'attachment; filename="homedex-context.md"',
        'X-Homedex-Context-Bytes': '18842',
        'X-Homedex-Truncation': '{"services":2,"hosts":0}'
      }
    })));

    const result = await loadContextExport();

    expect(fetch).toHaveBeenCalledWith('/api/export/context?include_private=false', { headers: { Accept: 'text/markdown' } });
    const exactBytes = new TextEncoder().encode(markdown).byteLength;
    expect(result).toMatchObject({
      markdown,
      bytes: exactBytes,
      size: `${exactBytes} B`,
      filename: 'homedex-context.md',
      title: 'Homedex lab context',
      schema: 'homedex.inventory.v1',
      counts: { hosts: 1, services: 2, ports: 1, routes: 1, expiry: 1 },
      truncation: { services: 2, hosts: 0 },
      sha256: '000102030405060708090A0B0C0D0E0F101112131415161718191A1B1C1D1E1F',
      shortSha256: '0001 0203 0405 … 1E1F'
    });
    expect(digest.mock.calls[0][0]).toBe('SHA-256');
    expect(digest.mock.calls[0][1]).toBeInstanceOf(ArrayBuffer);
    expect(new TextDecoder().decode(digest.mock.calls[0][1] as ArrayBuffer)).toBe(markdown);
  });
});
