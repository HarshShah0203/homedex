// The first-run wizard at /setup offers one source, Docker, and needs it to
// pass a connection test. A lab with no Docker endpoint Homedex can reach
// (Umbrel, which allows no socket access, or an install without the socket
// proxy) starts from Proxmox, Tailscale, SSH or a reverse proxy instead, all
// of which the Sources page adds. So the wizard can be skipped, and an empty
// inventory only sends the home page to it, never another page: skipping
// once cannot turn into a redirect loop.

const SKIP_KEY = 'homedex-setup-skipped';

export function setupSkipped(): boolean {
  try {
    return localStorage.getItem(SKIP_KEY) === '1';
  } catch {
    return false;
  }
}

export function skipSetup(): void {
  try {
    localStorage.setItem(SKIP_KEY, '1');
  } catch {
    // Without storage the skip is not remembered, but the wizard only opens
    // from the home page, so Sources stays reachable.
  }
}

export interface FirstRunState {
  pathname: string;
  authRequired: boolean;
  readOnly: boolean;
  hasInventory: boolean;
  connectors: number;
  issues: number;
  skipped: boolean;
}

export function shouldOpenSetup(state: FirstRunState): boolean {
  return (
    state.pathname === '/' &&
    !state.skipped &&
    !state.authRequired &&
    !state.readOnly &&
    !state.hasInventory &&
    state.connectors === 0 &&
    state.issues === 0
  );
}
