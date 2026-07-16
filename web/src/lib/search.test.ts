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
  });

  it('returns no groups for an empty query', () => {
    const inventory = createDemoInventory();
    expect(buildSearchGroups(inventory, '   ')).toEqual([]);
  });
});
