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
});
