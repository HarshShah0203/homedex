import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import Pages from './Pages.svelte';
import { createDemoInventory } from './demo';

afterEach(cleanup);

describe('final Homedex registers', () => {
  it('renders the editorial service index instead of dashboard cards', () => {
    const inventory = createDemoInventory();
    render(Pages, { props: { path: '/', inventory } });
    expect(screen.getByRole('heading', { name: /Services/ })).toBeInTheDocument();
    expect(screen.getByText('immich-server')).toBeInTheDocument();
    expect(screen.getByText('11 of 11 records')).toBeInTheDocument();
  });

  it('separates a broken route join from its provenance evidence', () => {
    const inventory = createDemoInventory();
    render(Pages, { props: { path: '/routes/7', inventory } });
    expect(screen.getByRole('heading', { name: 'old.lab.example', level: 1 })).toBeInTheDocument();
    expect(screen.getByText('Broken · no match')).toBeInTheDocument();
    expect(screen.getByText(/No current container address/)).toBeInTheDocument();
    expect(screen.getByText('TLS FACT')).toBeInTheDocument();
  });

  it('selects routes by record ID and preserves same-domain path outcomes', () => {
    const inventory = createDemoInventory();
    const shared = {
      ...inventory.routes[0],
      id: 99,
      domain: 'custom.lab.example',
      path_prefix: '/photos',
      proxy: 'Caddy',
      upstream_host: 'custom-service',
      upstream_port: 9443,
      resolved_service_id: 77,
      service: 'custom-service',
      resolve_confidence: 'medium',
      status: 'resolved'
    };
    inventory.routes.push(shared, { ...shared, id: 100, path_prefix: '/admin', resolved_service_id: null, service: '', status: 'broken', resolve_confidence: 'none' });

    render(Pages, { props: { path: '/routes/100?view=evidence', inventory } });

    expect(screen.getByRole('heading', { name: 'custom.lab.example', level: 1 })).toBeInTheDocument();
    expect(screen.getByText('custom-service:9443')).toBeInTheDocument();
    expect(screen.getByText('HTTPS · /admin')).toBeInTheDocument();
    expect(screen.getByText('Broken · no match')).toBeInTheDocument();
    expect(screen.getAllByText('Caddy').length).toBeGreaterThan(0);
    expect(screen.getByText('RTE-100 · ACTIVE · BROKEN')).toBeInTheDocument();
  });

  it('does not present unknown or ambiguous route states as resolved', () => {
    const inventory = createDemoInventory();
    inventory.routes[0] = { ...inventory.routes[0], domain: 'unknown.lab', status: 'unknown', resolved_service_id: null, service: '' };
    inventory.routes.push({ ...inventory.routes[0], id: 101, domain: 'ambiguous.lab', status: 'ambiguous' });

    const { unmount } = render(Pages, { props: { path: `/routes/${inventory.routes[0].id}`, inventory } });
    expect(screen.getByText('Unknown · not resolved')).toBeInTheDocument();
    unmount();
    render(Pages, { props: { path: '/routes/101', inventory } });
    expect(screen.getByText('Ambiguous · no unique match')).toBeInTheDocument();
    expect(screen.getAllByText(/did not guess/).length).toBeGreaterThan(0);
  });

  it('recognizes a successful connector result', () => {
    const inventory = createDemoInventory();
    inventory.connectors = [{ ...inventory.connectors[0], last_status: 'success' }];
    render(Pages, { props: { path: '/sources', inventory } });
    expect(screen.getByText('1 SOURCES · 1 CONNECTED')).toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
  });
});
