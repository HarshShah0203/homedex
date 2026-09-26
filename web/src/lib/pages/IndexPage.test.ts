import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import { createDemoInventory } from '../demo';
import type { Inventory } from '../types';
import IndexPage from './IndexPage.svelte';
import ChangesPage from './ChangesPage.svelte';

afterEach(cleanup);

const running = 'sha256:0000000000000000000000000000000000000000000000000000000000000000';
const published = 'sha256:1111111111111111111111111111111111111111111111111111111111111111';

function checkedInventory(): Inventory {
  const inventory = createDemoInventory();
  inventory.source = 'api';
  const checkedAt = new Date(Date.now() - 3 * 3600 * 1000).toISOString();
  inventory.services = inventory.services.map((service) => {
    switch (service.name) {
      case 'jellyfin':
      case 'grafana':
        return { ...service, update_status: 'update_available', update_checked_at: checkedAt, running_digest: running, latest_digest: published };
      case 'pihole':
        return { ...service, update_status: 'up_to_date', update_checked_at: checkedAt };
      case 'paperless-web':
        return { ...service, update_status: 'unknown', update_checked_at: checkedAt, update_reason: 'The registry requires credentials for this image; private registries are not checked.' };
      default:
        return { ...service, update_status: null };
    }
  });
  return inventory;
}

describe('service ledger image updates', () => {
  it('badges only services with a newer image published for their tag', () => {
    render(IndexPage, { props: { inventory: checkedInventory() } });
    expect(screen.getAllByText('update')).toHaveLength(2);
    const row = screen.getByText('jellyfin').closest('article')!;
    expect(row.querySelector('.update-badge')).not.toBeNull();
    const image = row.querySelector('[data-label="Image"]')!;
    expect(image.getAttribute('title')).toContain('Update available, checked 3h ago: running sha256:000000000000, published sha256:111111111111');
    expect(screen.getByText('pihole').closest('article')!.querySelector('.update-badge')).toBeNull();
    const unknown = screen.getByText('paperless-web').closest('article')!.querySelector('[data-label="Image"]')!;
    expect(unknown.getAttribute('title')).toContain('private registries are not checked');
  });

  it('filters the ledger to services with an update available', async () => {
    render(IndexPage, { props: { inventory: checkedInventory() } });
    const filter = screen.getByRole('button', { name: /Update available/ });
    expect(filter).toHaveAttribute('aria-pressed', 'false');
    expect(filter).toHaveTextContent('2');
    await fireEvent.click(filter);
    expect(filter).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('2 of 11 records')).toBeInTheDocument();
    expect(screen.queryByText('immich-server')).not.toBeInTheDocument();
    expect(screen.getByText('grafana')).toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText('Filter services'), { target: { value: 'zzz' } });
    expect(screen.getByText('No service with an update contains “zzz”.')).toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText('Filter services'), { target: { value: '' } });
    await fireEvent.click(filter);
    expect(screen.getByText('11 of 11 records')).toBeInTheDocument();
  });

  it('shows no update filter until a registry source has checked an image', () => {
    const inventory = createDemoInventory();
    render(IndexPage, { props: { inventory } });
    expect(screen.queryByRole('button', { name: /Update available/ })).not.toBeInTheDocument();
    expect(screen.queryByText('update')).not.toBeInTheDocument();
  });

  it('labels an image change-feed entry as an available update', () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    inventory.changes = [{ id: 21, scan_run_id: 4, entity_type: 'image', entity_id: 1, change_kind: 'modified', summary: 'Update available for traefik/whoami:v1.10.0', diff: { digest: { before: 'sha256:000000000000', after: 'sha256:111111111111' }, services: 1 }, seen: false, created_at: '2026-09-26T06:00:00Z' }];
    render(ChangesPage, { props: { inventory } });
    expect(screen.getByText('image update')).toBeInTheDocument();
    expect(screen.getByText('digest sha256:000000000000 → sha256:111111111111 · services 1')).toBeInTheDocument();
  });
});
