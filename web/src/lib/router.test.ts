import { afterEach, describe, expect, it } from 'vitest';
import { route } from './router';

afterEach(() => history.replaceState({}, '', '/'));

describe('router', () => {
  it('reads a directory address with a trailing slash as the same route', () => {
    let current = '';
    const stop = route.subscribe((value) => (current = value));
    for (const [address, want] of [['/ports/', '/ports'], ['/routes/7/', '/routes/7'], ['/hosts//', '/hosts'], ['/', '/'], ['/expiry/?q=tls', '/expiry?q=tls']]) {
      history.replaceState({}, '', address);
      window.dispatchEvent(new Event('homedex:navigate'));
      expect(current).toBe(want);
    }
    stop();
  });
});
