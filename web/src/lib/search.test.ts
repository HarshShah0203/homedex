import { describe, expect, it } from 'vitest';
import * as demo from './demo';
import { buildSearchGroups } from './search';

const inventory = { ...demo, source: 'demo' as const };

describe('buildSearchGroups', () => {
  it('groups connected inventory matches and explains why each record matched', () => {
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
    expect(buildSearchGroups(inventory, '   ')).toEqual([]);
  });
});
