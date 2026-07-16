import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import Pages from './Pages.svelte';
import { createDemoInventory } from './demo';

describe('final Homedex registers', () => {
  it('renders the editorial service index instead of dashboard cards', () => {
    const inventory = createDemoInventory();
    render(Pages, { props: { path: '/', inventory } });
    expect(screen.getByRole('heading', { name: 'Everything, in its place.' })).toBeInTheDocument();
    expect(screen.getByText('immich-server')).toBeInTheDocument();
    expect(screen.getByText('VIRTUAL WINDOW · 11 RECORDS RENDERED')).toBeInTheDocument();
  });

  it('separates a broken route join from its provenance evidence', () => {
    const inventory = createDemoInventory();
    render(Pages, { props: { path: '/routes/old.lab.example', inventory } });
    expect(screen.getByRole('heading', { name: 'old.lab.example', level: 1 })).toBeInTheDocument();
    expect(screen.getByText('Broken · no match')).toBeInTheDocument();
    expect(screen.getByText(/No current container address/)).toBeInTheDocument();
    expect(screen.getByText('TLS FACT')).toBeInTheDocument();
  });

  it('selects an arbitrary inventory route from the decoded URL path', () => {
    const inventory = createDemoInventory();
    inventory.routes.push({
      ...inventory.routes[0],
      id: 99,
      domain: 'custom.lab.example',
      proxy: 'Caddy',
      upstream_host: 'custom-service',
      upstream_port: 9443,
      resolved_service_id: 77,
      service: 'custom-service',
      resolve_confidence: 'medium',
      status: 'resolved'
    });

    render(Pages, { props: { path: '/routes/custom%2Elab%2Eexample?view=evidence', inventory } });

    expect(screen.getByRole('heading', { name: 'custom.lab.example', level: 1 })).toBeInTheDocument();
    expect(screen.getByText('custom-service:9443')).toBeInTheDocument();
    expect(screen.getByText('Resolved · medium confidence')).toBeInTheDocument();
    expect(screen.getAllByText('Caddy').length).toBeGreaterThan(0);
    expect(screen.getByText('RTE-099 · ACTIVE · RESOLVED')).toBeInTheDocument();
  });

  it('does not present unknown or ambiguous route states as resolved', () => {
    const inventory = createDemoInventory();
    inventory.routes[0] = { ...inventory.routes[0], domain: 'unknown.lab', status: 'unknown', resolved_service_id: null, service: '' };
    inventory.routes.push({ ...inventory.routes[0], id: 101, domain: 'ambiguous.lab', status: 'ambiguous' });

    const { unmount } = render(Pages, { props: { path: '/routes/unknown.lab', inventory } });
    expect(screen.getByText('Unknown · not resolved')).toBeInTheDocument();
    unmount();
    render(Pages, { props: { path: '/routes/ambiguous.lab', inventory } });
    expect(screen.getByText('Ambiguous · no unique match')).toBeInTheDocument();
    expect(screen.getAllByText(/did not guess/).length).toBeGreaterThan(0);
  });
});
