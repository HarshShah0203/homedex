import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import SharesPanel from './SharesPanel.svelte';
import type { Share } from './types';

afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

function share(overrides: Partial<Share> = {}): Share {
  return { id: 5, name: 'Wiki link', created_at: '2026-07-18T00:00:00Z', expires_at: null, active: true, ...overrides };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('SharesPanel', () => {
  it('lists shares loaded from the API', async () => {
    const fetchMock = vi.fn(async () => json({ items: [share()], total: 1 }));
    vi.stubGlobal('fetch', fetchMock);

    render(SharesPanel, { props: { readOnly: false } });

    expect(await screen.findByText('Wiki link')).toBeInTheDocument();
    expect(screen.getByText(/no expiry/)).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/api/share', expect.objectContaining({ method: 'GET' }));
  });

  it('creates a share, shows the one-time URL, and refreshes the list', async () => {
    const store: Share[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (path === '/api/share' && method === 'POST') {
        const created = share({ id: 9, name: 'Ops link', token: 'secret-token', share_url: '/share/secret-token' });
        store.push(share({ id: 9, name: 'Ops link' }));
        return json(created, 201);
      }
      return json({ items: store, total: store.length });
    });
    vi.stubGlobal('fetch', fetchMock);
    Object.defineProperty(window, 'location', { configurable: true, value: { origin: 'https://lab.example' } });

    render(SharesPanel, { props: { readOnly: false } });

    expect(await screen.findByText('NO SHARES')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Create share' }));
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'Ops link' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create share' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/share', expect.objectContaining({ method: 'POST', body: expect.stringContaining('Ops link') })));
    expect(await screen.findByText('https://lab.example/share/secret-token')).toBeInTheDocument();
    expect(screen.getByText(/This link is shown once/)).toBeInTheDocument();
    expect(await screen.findByText('Ops link')).toBeInTheDocument();
  });

  it('requires an inline confirm before revoking and calls DELETE', async () => {
    const store: Share[] = [share()];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'DELETE') {
        store.length = 0;
        return new Response(null, { status: 204 });
      }
      return json({ items: store, total: store.length });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(SharesPanel, { props: { readOnly: false } });

    await fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    expect(fetchMock).not.toHaveBeenCalledWith('/api/share/5', expect.objectContaining({ method: 'DELETE' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Confirm revoke' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/share/5', expect.objectContaining({ method: 'DELETE' })));
    expect(await screen.findByText('NO SHARES')).toBeInTheDocument();
  });

  it('renders nothing in read-only mode', async () => {
    const fetchMock = vi.fn(async () => json({ items: [share()], total: 1 }));
    vi.stubGlobal('fetch', fetchMock);

    const { container } = render(SharesPanel, { props: { readOnly: true } });

    expect(container.querySelector('[data-component-id="shares-panel"]')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
