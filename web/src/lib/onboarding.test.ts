import { afterEach, describe, expect, it } from 'vitest';
import { setupSkipped, shouldOpenSetup, skipSetup, type FirstRunState } from './onboarding';

const empty: FirstRunState = { pathname: '/', authRequired: false, readOnly: false, hasInventory: false, connectors: 0, issues: 0, skipped: false };

afterEach(() => localStorage.clear());

describe('first-run setup', () => {
  it('opens the wizard from the home page of an empty instance', () => {
    expect(shouldOpenSetup(empty)).toBe(true);
  });

  it('never redirects another page, so Sources is always reachable', () => {
    for (const pathname of ['/sources', '/hosts', '/copy-my-lab', '/setup']) {
      expect(shouldOpenSetup({ ...empty, pathname })).toBe(false);
    }
  });

  it('stays out of the way once skipped, or once there is anything to show', () => {
    expect(shouldOpenSetup({ ...empty, skipped: true })).toBe(false);
    expect(shouldOpenSetup({ ...empty, connectors: 1 })).toBe(false);
    expect(shouldOpenSetup({ ...empty, hasInventory: true })).toBe(false);
    expect(shouldOpenSetup({ ...empty, issues: 1 })).toBe(false);
    expect(shouldOpenSetup({ ...empty, authRequired: true })).toBe(false);
    expect(shouldOpenSetup({ ...empty, readOnly: true })).toBe(false);
  });

  it('remembers a skip in this browser', () => {
    expect(setupSkipped()).toBe(false);
    skipSetup();
    expect(setupSkipped()).toBe(true);
  });
});
