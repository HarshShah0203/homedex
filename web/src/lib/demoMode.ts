// The static live demo (`npm run build:demo`) is this same app with no
// backend: it serves the fabricated inventory from demo.ts and refuses every
// write. Vite replaces import.meta.env.MODE at build time, so in the embedded
// production bundle DEMO_MODE is the constant false and its branches drop out.
export const DEMO_MODE = import.meta.env.MODE === 'demo';

export const REPO_URL = 'https://github.com/HarshShah0203/homedex';
export const INSTALL_URL = `${REPO_URL}#quickstart`;

export const DEMO_REFUSED_EVENT = 'homedex:demo-refused';

export class DemoActionError extends Error {
  constructor() {
    super('Turned off in the live demo.');
    this.name = 'DemoActionError';
  }
}

// Every mutating API call lands here in the demo. The short message is shown
// next to the control the visitor used, and the event lets the demo banner
// explain why and point at the install instructions.
export function refuseInDemo(): never {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(DEMO_REFUSED_EVENT));
  throw new DemoActionError();
}
