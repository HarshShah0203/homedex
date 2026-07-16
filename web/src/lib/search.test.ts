import { describe, expect, it } from 'vitest';
import { createDemoInventory } from './demo';
import { buildSearchGroups } from './search';

describe('buildSearchGroups', () => {
  it('groups connected inventory matches and explains why each record matched', () => {
    const inventory = createDemoInventory();
    const groups = buildSearchGroups(inventory, 'immich');
    expect(groups.map((group) => group.label)).toEqual(['Services', 'Routes', 'Ports', 'Hosts']);
    expect(groups.flatMap((group) => group.results.map((result) => result.reason))).toEqual([
      'Name starts with ‘immich’',
      'Stack equals ‘immich’',
      'Connected to immich-server',
      'Declared by immich-server',
      'Hosts 2 matching records'
    ]);
    expect(groups.find((group) => group.label === 'Routes')?.results[0].href).toBe('/routes/1');
  });

  it('keeps same-domain path routes distinct with canonical ID links', () => {
    const inventory = createDemoInventory();
    const base = { ...inventory.routes[0], domain: 'shared.lab.example' };
    inventory.routes = [
      { ...base, id: 40, path_prefix: '/app', status: 'resolved' },
      { ...base, id: 41, path_prefix: '/admin', status: 'broken', resolved_service_id: null, service: '' }
    ];

    const routes = buildSearchGroups(inventory, 'shared.lab.example').find((group) => group.label === 'Routes')?.results;

    expect(routes).toEqual([
      expect.objectContaining({ title: 'shared.lab.example/app', href: '/routes/40', state: 'resolved' }),
      expect.objectContaining({ title: 'shared.lab.example/admin', href: '/routes/41', state: 'broken' })
    ]);
  });

  it('returns no groups for an empty query', () => {
    const inventory = createDemoInventory();
    expect(buildSearchGroups(inventory, '   ')).toEqual([]);
  });
});
