import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import Pages from './Pages.svelte';
import * as demo from './demo';

const inventory = { ...demo, source: 'demo' as const };

describe('final Homedex registers', () => {
  it('renders the editorial service index instead of dashboard cards', () => {
    render(Pages, { props: { path: '/', inventory } });
    expect(screen.getByRole('heading', { name: 'Everything, in its place.' })).toBeInTheDocument();
    expect(screen.getByText('immich-server')).toBeInTheDocument();
    expect(screen.getByText('VIRTUAL WINDOW · 11 RECORDS RENDERED')).toBeInTheDocument();
  });

  it('separates a broken route join from its provenance evidence', () => {
    render(Pages, { props: { path: '/routes/old.lab.example', inventory } });
    expect(screen.getByRole('heading', { name: 'old.lab.example', level: 1 })).toBeInTheDocument();
    expect(screen.getByText('Broken · no match')).toBeInTheDocument();
    expect(screen.getByText(/No current container address/)).toBeInTheDocument();
    expect(screen.getByText('TLS FACT')).toBeInTheDocument();
  });
});
