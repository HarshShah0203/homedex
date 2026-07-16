import { afterEach, describe, expect, it, vi } from 'vitest';
import { getSetupStatus, loadInventory, login } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('loadInventory', () => {
  it('uses the designed demo inventory when the API is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
    const inventory = await loadInventory();
    expect(inventory.source).toBe('demo');
    expect(inventory.services[0].name).toBe('immich-server');
    expect(inventory.error).toContain('offline');
  });

  it('joins host and service names into live API records', async () => {
    const payloads: Record<string, unknown[]> = {
      services: [{ id: 8, name: 'actual-service', host_id: 3, state: 'running', stack: '', image: 'repo/image', tag: 'v1', first_seen: '', last_seen: 'now', kind: 'container', natural_key: 'svc' }],
      hosts: [{ id: 3, name: 'actual-host', kind: 'docker', address: '10.0.0.3', os: 'Linux', arch: 'amd64', state: 'active', first_seen: '', last_seen: 'now', natural_key: 'host' }],
      ports: [{ id: 1, service_id: 8, host_id: 3, number: 8080, protocol: 'tcp', published: 1, host_ip: '0.0.0.0', container_port: 80, source: 'docker' }],
      routes: [], changes: [], certs: [], domains: [], connectors: []
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const key = String(input).match(/\/api\/([^?]+)/)?.[1] ?? '';
      return new Response(JSON.stringify({ items: payloads[key], total: payloads[key]?.length ?? 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));
    const inventory = await loadInventory();
    expect(inventory.source).toBe('api');
    expect(inventory.services[0].host).toBe('actual-host');
    expect(inventory.ports[0]).toMatchObject({ service: 'actual-service', host: 'actual-host', published: true });
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
