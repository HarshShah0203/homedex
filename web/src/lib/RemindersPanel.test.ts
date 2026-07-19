import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import RemindersPanel from './RemindersPanel.svelte';
import type { NotificationRule } from './types';

afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

function rule(overrides: Partial<NotificationRule> = {}): NotificationRule {
  return { id: 7, name: 'Expiry 30d', kind: 'expiry', threshold_days: 30, filters: {}, channels: ['ntfy'], channel_count: 1, enabled: true, created_at: '', updated_at: '', ...overrides };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('RemindersPanel', () => {
  it('renders rules loaded from the API', async () => {
    const fetchMock = vi.fn(async () => json({ items: [rule()], total: 1 }));
    vi.stubGlobal('fetch', fetchMock);

    render(RemindersPanel, { props: { readOnly: false } });

    expect(await screen.findByText('30d before')).toBeInTheDocument();
    expect(screen.getByText('ntfy')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/api/notify/rules', expect.objectContaining({ method: 'GET' }));
  });

  it('adds a reminder and refreshes the list', async () => {
    const store: NotificationRule[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (path === '/api/notify/rules' && method === 'POST') {
        const created = rule({ threshold_days: 21, channels: ['ntfy'] });
        store.push(created);
        return json(created, 201);
      }
      return json({ items: store, total: store.length });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(RemindersPanel, { props: { readOnly: false } });

    expect(await screen.findByText('NO REMINDERS')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Add reminder' }));
    await fireEvent.input(screen.getByLabelText('Days before'), { target: { value: '21' } });
    await fireEvent.input(screen.getByLabelText('Shoutrrr URL'), { target: { value: 'ntfy://ntfy.sh/my-lab' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add reminder' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/notify/rules', expect.objectContaining({ method: 'POST', body: expect.stringContaining('ntfy://ntfy.sh/my-lab') })));
    expect(await screen.findByText('21d before')).toBeInTheDocument();
  });

  it('adds a change-kind reminder with a null threshold', async () => {
    const store: NotificationRule[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (path === '/api/notify/rules' && method === 'POST') {
        const created = rule({ id: 8, name: 'Changes', kind: 'change', threshold_days: null, channels: ['ntfy'] });
        store.push(created);
        return json(created, 201);
      }
      return json({ items: store, total: store.length });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(RemindersPanel, { props: { readOnly: false } });

    expect(await screen.findByText('NO REMINDERS')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Add reminder' }));
    await fireEvent.change(screen.getByLabelText('Kind'), { target: { value: 'change' } });
    expect(screen.queryByLabelText('Days before')).not.toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText('Shoutrrr URL'), { target: { value: 'ntfy://ntfy.sh/my-lab' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add reminder' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/notify/rules', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ name: 'Changes', kind: 'change', threshold_days: null, channels: ['ntfy://ntfy.sh/my-lab'] })
    })));
    expect(await screen.findByText('on changes')).toBeInTheDocument();
  });

  it('requires an inline confirm before deleting and calls the API', async () => {
    const store: NotificationRule[] = [rule()];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'DELETE') {
        store.length = 0;
        return new Response(null, { status: 204 });
      }
      return json({ items: store, total: store.length });
    });
    vi.stubGlobal('fetch', fetchMock);

    render(RemindersPanel, { props: { readOnly: false } });

    await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(fetchMock).not.toHaveBeenCalledWith('/api/notify/rules/7', expect.objectContaining({ method: 'DELETE' }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Confirm delete' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/notify/rules/7', expect.objectContaining({ method: 'DELETE' })));
    expect(await screen.findByText('NO REMINDERS')).toBeInTheDocument();
  });

  it('hides mutation controls in read-only mode', async () => {
    const fetchMock = vi.fn(async () => json({ items: [rule()], total: 1 }));
    vi.stubGlobal('fetch', fetchMock);

    render(RemindersPanel, { props: { readOnly: true } });

    expect(await screen.findByText('30d before')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add reminder' })).not.toBeInTheDocument();
  });
});
