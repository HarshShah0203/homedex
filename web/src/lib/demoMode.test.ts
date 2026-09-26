import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';

// Vite fixes import.meta.env when it builds, so these tests stub the env and
// load fresh module copies, the way `npm run build:demo` would see them.
async function loadAs(env: { MODE?: string; BASE_URL?: string }) {
  for (const [name, value] of Object.entries(env)) vi.stubEnv(name, value);
  vi.resetModules();
  return {
    api: await import('./api'),
    demoMode: await import('./demoMode'),
    router: await import('./router'),
    demo: await import('./demo')
  };
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
  window.history.replaceState({}, '', '/');
});

describe('live demo build', () => {
  it('answers every read from the fabricated inventory without a backend', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    // jsdom's SubtleCrypto rejects buffers from Node's realm; the browser does not.
    vi.stubGlobal('crypto', { subtle: { digest: vi.fn().mockResolvedValue(new Uint8Array(32).buffer) } });
    const { api } = await loadAs({ MODE: 'demo' });

    const inventory = await api.loadInventory({ demoOnEmpty: false });
    expect(inventory.source).toBe('demo');
    expect(inventory.services.map((service) => service.name)).toContain('immich-server');
    expect(await api.getSetupStatus()).toEqual({ configured: true, auth_disabled: true });
    expect(await api.loadPortConflicts()).toEqual([]);
    expect(await api.loadNextFreePort(3)).toBe(1024);
    expect(await api.loadConnectorScans(1)).toEqual([]);
    expect((await api.loadNotificationRules()).length).toBeGreaterThan(0);
    expect((await api.loadShares()).length).toBeGreaterThan(0);

    const context = await api.loadContextExport();
    expect(context.title).toBe('Homedex lab context');
    expect(context.schema).toBe('homedex.inventory.v1');
    expect(context.counts).toEqual({
      services: inventory.services.length,
      hosts: inventory.hosts.length,
      routes: inventory.routes.length,
      ports: inventory.ports.length,
      expiry: inventory.expiries.length
    });
    expect(context.markdown).toContain('| photos.lab.example | / | Nginx Proxy Manager | nas-01:2283 | immich-server | true | ok |');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('refuses every write with a friendly message and never reaches the network', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const { api, demoMode } = await loadAs({ MODE: 'demo' });
    const refused = vi.fn();
    window.addEventListener(demoMode.DEMO_REFUSED_EVENT, refused);

    const writes: Array<() => Promise<unknown>> = [
      () => api.login('password'),
      () => api.testConnector({ name: 'Docker', kind: 'docker', config: {} }),
      () => api.createConnector({ name: 'Docker', kind: 'docker', config: {} }),
      () => api.updateConnector(1, { enabled: false }),
      () => api.deleteConnector(1),
      () => api.testSavedConnector(1),
      () => api.scanConnector(1),
      () => api.reviewChange(1, true),
      () => api.reviewChanges([1, 2], true),
      () => api.createNotificationRule({ name: 'Expiry 7d', kind: 'expiry', threshold_days: 7, channels: ['ntfy://ntfy.sh/lab'] }),
      () => api.deleteNotificationRule(1),
      () => api.testNotificationRule(1),
      () => api.createShare({ name: 'Wiki' }),
      () => api.revokeShare(1),
      () => api.createManualEntity({ entity_type: 'host', name: 'printer' }),
      () => api.patchEntity('host', 1, { notes: 'rack 2' })
    ];
    try {
      for (const write of writes) await expect(write()).rejects.toThrow('Turned off in the live demo.');
    } finally {
      window.removeEventListener(demoMode.DEMO_REFUSED_EVENT, refused);
    }
    expect(refused).toHaveBeenCalledTimes(writes.length);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('keeps anchoring fabricated dates to today', async () => {
    const { demo } = await loadAs({});
    const now = new Date('2031-03-01T12:00:00Z');
    const inventory = demo.createDemoInventory(undefined, now);
    const soonest = inventory.expiries.find((record) => record.days_remaining === 14);

    expect(soonest?.expires_at).toBe('2031-03-15T12:00:00.000Z');
    expect(inventory.connectors.every((connector) => connector.updated_at === '2031-03-01T11:58:00.000Z')).toBe(true);
  });
});

describe('router base path', () => {
  it('maps a sub-path address bar to root-relative app routes', async () => {
    window.history.replaceState({}, '', '/homedex/routes/7?view=evidence');
    const { router } = await loadAs({ MODE: 'demo', BASE_URL: '/homedex/' });

    expect(get(router.route)).toBe('/routes/7?view=evidence');
    expect(router.appHref('/ports')).toBe('/homedex/ports');

    const seen: string[] = [];
    const stop = router.route.subscribe((value) => seen.push(value));
    router.navigate('/ports');
    stop();
    expect(window.location.pathname).toBe('/homedex/ports');
    expect(seen.at(-1)).toBe('/ports');
  });

  it('treats the bare base as the index', async () => {
    window.history.replaceState({}, '', '/homedex');
    const { router } = await loadAs({ BASE_URL: '/homedex/' });
    expect(get(router.route)).toBe('/');
  });

  it('leaves root-served paths untouched in the production build', async () => {
    window.history.replaceState({}, '', '/homedex/ports');
    const { router, demoMode } = await loadAs({ MODE: 'production', BASE_URL: '/' });

    expect(demoMode.DEMO_MODE).toBe(false);
    expect(get(router.route)).toBe('/homedex/ports');
    expect(router.appHref('/routes/7')).toBe('/routes/7');
  });
});
