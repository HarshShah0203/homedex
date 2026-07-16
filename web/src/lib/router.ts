import { readable } from 'svelte/store';

function currentLocation() {
  return `${window.location.pathname}${window.location.search}`;
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
  history[method]({}, '', path);
  window.dispatchEvent(new Event('homedex:navigate'));
}
