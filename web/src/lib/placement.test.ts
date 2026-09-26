import { describe, expect, it } from 'vitest';
import { placement } from './placement';
import type { Host } from './types';

const base: Host = { id: 1, name: 'host', kind: 'docker', address: '', os: '', arch: '', state: 'active' };

describe('placement', () => {
  it('names a guest, the node it runs on and its power state', () => {
    expect(placement({ ...base, kind: 'vm', parent: 'pve-01', parent_id: 4, power_state: 'running' })).toBe('VM on pve-01 · running');
    expect(placement({ ...base, kind: 'lxc', parent: 'pve-01', power_state: 'stopped' })).toBe('Container on pve-01 · stopped');
    expect(placement({ ...base, kind: 'proxmox-node', power_state: 'online' })).toBe('Proxmox node · online');
    expect(placement({ ...base, kind: 'vm', power_state: '' })).toBe('VM');
  });

  it('says nothing for hosts that report no placement', () => {
    expect(placement(base)).toBe('');
    expect(placement({ ...base, kind: 'tailscale', power_state: '', parent: '' })).toBe('');
  });
});
