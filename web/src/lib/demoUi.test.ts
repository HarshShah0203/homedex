import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import DemoBanner from './DemoBanner.svelte';
import Pages from './Pages.svelte';
import SourcesPage from './pages/SourcesPage.svelte';
import { createDemoInventory } from './demo';
import { DEMO_MODE, DEMO_REFUSED_EVENT } from './demoMode';

// Vite fixes MODE when it builds; set it before any app module loads, the way
// `npm run build:demo` sees it.
vi.hoisted(() => {
  vi.stubEnv('MODE', 'demo');
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('live demo screens', () => {
  it('runs in demo mode', () => {
    expect(DEMO_MODE).toBe(true);
  });

  it('labels the demo and explains a refused action with an install link', async () => {
    render(DemoBanner);
    expect(screen.getByText('Live demo with fabricated data')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Install Homedex' })).toHaveAttribute('href', 'https://github.com/HarshShah0203/homedex#quickstart');

    window.dispatchEvent(new Event(DEMO_REFUSED_EVENT));
    expect(await screen.findByText('Nothing is saved in the live demo.')).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: 'Install Homedex' })).toHaveLength(2);

    await fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('Nothing is saved in the live demo.')).not.toBeInTheDocument();
  });

  it('shows the refusal next to the control the visitor used', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(SourcesPage, { props: { inventory: createDemoInventory() } });
    await fireEvent.click(screen.getAllByRole('button', { name: 'Scan now' })[0]);

    expect(await screen.findByText('Turned off in the live demo.')).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('replaces the setup wizard with install directions', () => {
    render(Pages, { props: { path: '/setup', inventory: createDemoInventory() } });

    expect(screen.getByRole('heading', { name: 'Setup runs on your own install', level: 1 })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Install Homedex' })).toHaveAttribute('href', 'https://github.com/HarshShah0203/homedex#quickstart');
    expect(screen.queryByText('Development demo inventory')).not.toBeInTheDocument();
  });
});
