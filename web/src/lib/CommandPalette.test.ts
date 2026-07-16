import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import CommandPalette from './CommandPalette.svelte';
import * as demo from './demo';

describe('CommandPalette', () => {
  it('renders grouped cmd-K results with explicit match reasons', () => {
    render(CommandPalette, { props: { open: true, inventory: { ...demo, source: 'demo' } } });
    expect(screen.getByRole('dialog', { name: 'Search every record' })).toBeInTheDocument();
    expect(screen.getByText(/Name starts with ‘immich’/)).toBeInTheDocument();
    expect(screen.getByText(/Stack equals ‘immich’/)).toBeInTheDocument();
    expect(screen.getByText(/Connected to immich-server/)).toBeInTheDocument();
    expect(screen.getByText(/Declared by immich-server/)).toBeInTheDocument();
    expect(screen.getByText(/Hosts 2 matching records/)).toBeInTheDocument();
  });
});
