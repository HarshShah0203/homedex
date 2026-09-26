import { afterEach, describe, expect, it, vi } from 'vitest';
import { createDemoInventory } from './demo';

afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});

// Vite fixes MODE when it builds; load a fresh api module the way
// `npm run build:demo` sees it.
async function demoApi() {
  vi.stubEnv('MODE', 'demo');
  vi.resetModules();
  vi.stubGlobal('fetch', vi.fn());
  return import('./api');
}

describe('live demo Proxmox source', () => {
  it('lists a node, its two guests and the source that found them', async () => {
    const api = await demoApi();
    const inventory = await api.loadInventory({ demoOnEmpty: false });

    const proxmox = inventory.hosts.filter((host) => ['proxmox-node', 'vm', 'lxc'].includes(host.kind));
    expect(proxmox.map((host) => `${host.name}:${host.kind}:${host.parent ?? ''}:${host.power_state}`)).toEqual([
      'pve-01:proxmox-node::online',
      'home-assistant:vm:pve-01:running',
      'unifi:lxc:pve-01:running'
    ]);
    const node = proxmox[0];
    expect(proxmox.slice(1).every((guest) => guest.parent_id === node.id)).toBe(true);
    expect(inventory.connectors.map((connector) => connector.kind)).toEqual(['docker', 'npm', 'caddy', 'registry', 'proxmox']);
    expect(inventory.connectors.find((connector) => connector.kind === 'proxmox')?.found).toBe('1 node · 2 guests');
    expect(new Set(inventory.hosts.map((host) => host.id)).size).toBe(inventory.hosts.length);
    expect(fetch).not.toHaveBeenCalled();
  });

  it('exports the same hosts the Hosts page lists', async () => {
    vi.stubGlobal('crypto', { subtle: { digest: vi.fn().mockResolvedValue(new Uint8Array(32).buffer) } });
    const api = await demoApi();
    const inventory = await api.loadInventory({ demoOnEmpty: false });
    const context = await api.loadContextExport();

    expect(context.counts.hosts).toBe(inventory.hosts.length);
    expect(context.markdown).toContain('| home-assistant | vm | 10.0.30.21 |');
  });

  it('is not added to the dev-server fallback', () => {
    expect(createDemoInventory().hosts.some((host) => ['proxmox-node', 'vm', 'lxc'].includes(host.kind))).toBe(false);
    expect(createDemoInventory().connectors.some((connector) => connector.kind === 'proxmox')).toBe(false);
  });
});
