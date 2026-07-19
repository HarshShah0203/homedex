import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createDemoInventory } from '../demo';
import SourcesPage from './SourcesPage.svelte';

afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('SourcesPage connector lifecycle', () => {
  it('tests, scans, edits, disables, and deletes an existing source', async () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    inventory.connectors = [inventory.connectors[0]];
    const onrefresh = vi.fn(async () => {});
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith('/test')) return new Response(JSON.stringify({ status: 'ok' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (path.endsWith('/scan')) return new Response(JSON.stringify({ status: 'success', scan_run_id: 44, changes: 2 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (init?.method === 'DELETE') return new Response(null, { status: 204 });
      return new Response(JSON.stringify({ connector: inventory.connectors[0], scan_run_id: 0, changes: 0, scan_error: '' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    });
    vi.stubGlobal('fetch', fetchMock);

    // Drive Date.now so the delete two-step confirm can advance past its 350ms guard.
    let now = 1_700_000_000_000;
    vi.spyOn(Date, 'now').mockImplementation(() => now);

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

    expect(fetchMock).not.toHaveBeenCalledWith('/api/connectors/1', expect.objectContaining({ method: 'DELETE' }));
    now += 400; // advance past the 350ms double-click guard so the confirm is honored
    await fireEvent.click(await screen.findByRole('button', { name: 'Confirm delete' }));
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

describe('SourcesPage add source', () => {
  function bodyOf(init?: RequestInit): Record<string, unknown> {
    return JSON.parse(String(init?.body ?? '{}'));
  }

  it('renders kind-specific fields when a kind is selected', async () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    render(SourcesPage, { props: { inventory } });

    await fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
    // Traefik exposes its URL field.
    await fireEvent.change(screen.getByLabelText('Source type'), { target: { value: 'traefik' } });
    expect(screen.getByLabelText('Traefik URL')).toBeInTheDocument();
    // RDAP swaps in a domains textarea.
    await fireEvent.change(screen.getByLabelText('Source type'), { target: { value: 'rdap' } });
    expect(screen.getByLabelText('Domains, one per line').tagName).toBe('TEXTAREA');
  });

  it('enables save only after the current settings pass a test', async () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ status: 'ok' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    );
    vi.stubGlobal('fetch', fetchMock);
    render(SourcesPage, { props: { inventory } });

    await fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
    await fireEvent.change(screen.getByLabelText('Source type'), { target: { value: 'traefik' } });
    await fireEvent.input(screen.getByLabelText('Traefik URL'), { target: { value: 'https://traefik.lab' } });

    expect(screen.getByRole('button', { name: 'Save and scan' })).toBeDisabled();
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    expect(await screen.findByRole('button', { name: 'Connection verified' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save and scan' })).toBeEnabled();
  });

  it('posts the right body from textarea arrays and refreshes', async () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    const onrefresh = vi.fn(async () => {});
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith('/connectors/test')) return new Response(JSON.stringify({ status: 'ok' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ connector: { id: 9 }, scan_run_id: 3, changes: 5, scan_error: '' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    });
    vi.stubGlobal('fetch', fetchMock);
    render(SourcesPage, { props: { inventory, onrefresh } });

    await fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
    await fireEvent.change(screen.getByLabelText('Source type'), { target: { value: 'rdap' } });
    await fireEvent.input(screen.getByLabelText('Domains, one per line'), { target: { value: ' example.com \n\n foo.dev \n' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    await screen.findByRole('button', { name: 'Connection verified' });
    await fireEvent.click(screen.getByRole('button', { name: 'Save and scan' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/connectors', expect.objectContaining({ method: 'POST' })));
    const createCall = fetchMock.mock.calls.find((call) => call[0] === '/api/connectors');
    const body = bodyOf(createCall?.[1]);
    expect(body.kind).toBe('rdap');
    expect(body.config).toEqual({ domains: ['example.com', 'foo.dev'] });
    expect(body.enabled).toBe(true);
    await waitFor(() => expect(onrefresh).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('Source added, 5 changes recorded.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save and scan' })).not.toBeInTheDocument();
  });

  it('treats a scan_error as created, collapsing and refreshing', async () => {
    const inventory = createDemoInventory();
    inventory.source = 'api';
    const onrefresh = vi.fn(async () => {});
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith('/connectors/test')) return new Response(JSON.stringify({ status: 'ok' }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ connector: { id: 9 }, scan_run_id: 0, changes: 0, scan_error: 'dial tcp refused' }), { status: 502, headers: { 'Content-Type': 'application/json' } });
    });
    vi.stubGlobal('fetch', fetchMock);
    render(SourcesPage, { props: { inventory, onrefresh } });

    await fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
    await fireEvent.change(screen.getByLabelText('Source type'), { target: { value: 'caddy' } });
    await fireEvent.input(screen.getByLabelText('Admin endpoint'), { target: { value: 'http://caddy:2019' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    await screen.findByRole('button', { name: 'Connection verified' });
    await fireEvent.click(screen.getByRole('button', { name: 'Save and scan' }));

    await waitFor(() => expect(onrefresh).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('button', { name: 'Save and scan' })).not.toBeInTheDocument();
    expect(await screen.findByText('Source added, first scan failed: dial tcp refused')).toBeInTheDocument();
  });
});
