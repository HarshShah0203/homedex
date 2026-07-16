import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createDemoInventory } from '../demo';
import SourcesPage from './SourcesPage.svelte';

afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

describe('SourcesPage connector lifecycle', () => {
  it('tests, scans, edits, disables, and deletes an existing source', async () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    inventory.connectors = [inventory.connectors[0]];
    const onrefresh = vi.fn(async () => {});
    const confirm = vi.fn(() => true);
    vi.stubGlobal('confirm', confirm);
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith('/test')) return new Response(JSON.stringify({ status: 'ok' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (path.endsWith('/scan')) return new Response(JSON.stringify({ status: 'success', scan_run_id: 44, changes: 2 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (init?.method === 'DELETE') return new Response(null, { status: 204 });
      return new Response(JSON.stringify({ connector: inventory.connectors[0], scan_run_id: 0, changes: 0, scan_error: '' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(SourcesPage, { props: { inventory, onrefresh } });

    await fireEvent.click(screen.getByRole('button', { name: 'Test' }));
    expect(await screen.findByText('Connection test passed.')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Scan now' }));
    expect(await screen.findByText('Scan 44 completed · 2 changes.')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await fireEvent.input(screen.getByLabelText('Source name'), { target: { value: 'Docker inventory' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/connectors/1', expect.objectContaining({ body: expect.stringContaining('Docker inventory') })));
    await fireEvent.click(screen.getByRole('button', { name: 'Disable' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/connectors/1', expect.objectContaining({ body: JSON.stringify({ enabled: false }) })));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    expect(confirm).toHaveBeenCalled();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/connectors/1', expect.objectContaining({ method: 'DELETE' })));
    await waitFor(() => expect(onrefresh).toHaveBeenCalledTimes(4));
  });

  it('hides connector mutation controls in shared mode', () => {
    const inventory = createDemoInventory();
    inventory.readOnly = true;
    render(SourcesPage, { props: { inventory } });
    expect(screen.queryByRole('button', { name: 'Scan now' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });
});
