import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import CopyLabPage from './CopyLabPage.svelte';

afterEach(() => {
  vi.unstubAllGlobals();
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });
});

describe('CopyLabPage', () => {
  it('renders and copies the exact backend context response', async () => {
    const markdown = `# Homedex lab context

Schema: \`homedex.inventory.v1\`

## Hosts

| Name | Kind |
|---|---|
| nas | docker |

## Services

| Host | Service |
|---|---|
| nas | photos |

## Ports

| Host | Service |
|---|---|

## Routes

| Domain | Service |
|---|---|

## Expiry

| Name | Type |
|---|---|

## Truncation report

- services: 0 omitted
`;
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    vi.stubGlobal('crypto', { subtle: { digest: vi.fn().mockResolvedValue(new Uint8Array(32).buffer) } });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(markdown, {
      status: 200,
      headers: {
        'Content-Disposition': 'attachment; filename="homedex-context.md"',
        'X-Homedex-Context-Bytes': String(new TextEncoder().encode(markdown).byteLength),
        'X-Homedex-Truncation': '{"hosts":0,"services":0,"ports":0,"routes":0,"expiry":0}'
      }
    })));

    render(CopyLabPage);

    const copy = screen.getByRole('button', { name: 'Copy Markdown' });
    await waitFor(() => expect(copy).toBeEnabled());
    expect(screen.getByRole('heading', { name: 'Homedex lab context', level: 2 })).toBeInTheDocument();
    expect(screen.getByText('SCHEMA HOMEDEX.INVENTORY.V1')).toBeInTheDocument();
    expect(screen.getByText('0000 0000 0000 … 0000')).toBeInTheDocument();

    await fireEvent.click(copy);

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(markdown));
    expect(screen.getByRole('button', { name: 'Markdown copied' })).toBeInTheDocument();
  });
});
