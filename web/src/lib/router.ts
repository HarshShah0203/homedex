import { readable } from 'svelte/store';

// Routes inside the app are always written from the root ("/routes/7"). The
// static live demo is served below a sub-path (GitHub Pages: /homedex/), so the
// router strips Vite's base from the address bar and adds it back to links.
// The embedded production build uses base "/", which makes both no-ops.
const BASE = import.meta.env.BASE_URL.replace(/\/+$/, '');

function currentLocation() {
  const { pathname, search } = window.location;
  const inside = BASE && (pathname === BASE || pathname.startsWith(`${BASE}/`)) ? pathname.slice(BASE.length) || '/' : pathname;
  return `${inside}${search}`;
}

export function appHref(path: string): string {
  return `${BASE}${path}`;
}

export const route = readable(currentLocation(), (set) => {
  const update = () => set(currentLocation());
  window.addEventListener('popstate', update);
  window.addEventListener('homedex:navigate', update);
  return () => {
    window.removeEventListener('popstate', update);
    window.removeEventListener('homedex:navigate', update);
  };
});

export function navigate(path: string, options: { replace?: boolean } = {}) {
  if (currentLocation() === path) return;
  const method = options.replace ? 'replaceState' : 'pushState';
  history[method]({}, '', appHref(path));
  window.dispatchEvent(new Event('homedex:navigate'));
}
