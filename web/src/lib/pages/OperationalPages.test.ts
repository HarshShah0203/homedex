import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createDemoInventory } from '../demo';
import ChangesPage from './ChangesPage.svelte';
import PortsPage from './PortsPage.svelte';

afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

describe('operational page actions', () => {
  it('persists a change review before showing it as reviewed', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    const inventory = createDemoInventory();
    inventory.source = 'api';

    render(ChangesPage, { props: { inventory } });
    await fireEvent.click(screen.getByRole('button', { name: 'Mark paperless-web was added reviewed' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/changes/1', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ seen: true })
    })));
    expect(await screen.findByText('CHG-001 · reviewed')).toBeInTheDocument();
  });

  it('requests the next-free port for the selected host', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const hostID = new URL(String(input), 'http://homedex.local').searchParams.get('host_id');
      return new Response(JSON.stringify({ port: hostID === '2' ? 2202 : 1101 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    });
    vi.stubGlobal('fetch', fetchMock);
    const inventory = createDemoInventory();
    inventory.source = 'api';

    render(PortsPage, { props: { inventory } });
    expect(await screen.findByText('1101')).toBeInTheDocument();
    await fireEvent.change(screen.getByRole('combobox', { name: 'Select host for port lookup' }), { target: { value: '2' } });

    expect(await screen.findByText('2202')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/api/ports/next-free?host_id=2&start=1024&end=65535&protocol=tcp', expect.any(Object));
  });

  it('hides change mutations in a shared inventory', () => {
    const inventory = createDemoInventory();
    inventory.readOnly = true;
    render(ChangesPage, { props: { inventory } });
    expect(screen.queryByRole('button', { name: /Mark .* reviewed/ })).not.toBeInTheDocument();
    expect(screen.getAllByText('READ ONLY').length).toBeGreaterThan(0);
  });

  it('renders readable factual differences from object diffs without a detail field', () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    inventory.changes = [
      { id: 10, scan_run_id: 1, entity_type: 'domain', entity_id: 1, change_kind: 'added', summary: 'Domain lab.example discovered', diff: { expires_at: '2027-02-18T00:00:00Z' }, seen: false, created_at: '2026-07-16T12:20:59Z' },
      { id: 11, scan_run_id: 1, entity_type: 'service', entity_id: 2, change_kind: 'modified', summary: 'Service immich changed', diff: { tag: { before: 'v1.134.0', after: 'v1.135.3' } }, seen: false, created_at: '2026-07-16T12:20:59Z' },
      { id: 12, scan_run_id: 1, entity_type: 'ports', entity_id: 3, change_kind: 'modified', summary: 'Published ports changed', diff: { before: [], after: ['a', 'b', 'c'] }, seen: false, created_at: '2026-07-16T12:20:59Z' },
      { id: 13, scan_run_id: 1, entity_type: 'host', entity_id: 4, change_kind: 'modified', summary: 'Host core-01 changed', diff: { metadata: { owner: 'lab' } }, seen: false, created_at: '2026-07-16T12:20:59Z' }
    ];

    render(ChangesPage, { props: { inventory } });

    expect(screen.queryByText(/\[object Object\]/)).not.toBeInTheDocument();
    expect(screen.getByText('expires_at 2027-02-18T00:00:00Z')).toBeInTheDocument();
    expect(screen.getByText('tag v1.134.0 → v1.135.3')).toBeInTheDocument();
    expect(screen.getByText('0 → 3 declarations')).toBeInTheDocument();
    expect(screen.getByText('metadata {"owner":"lab"}')).toBeInTheDocument();
  });
});
