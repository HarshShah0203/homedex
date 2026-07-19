import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import { createDemoInventory } from '../demo';
import RoutePage from './RoutePage.svelte';

afterEach(() => {
  cleanup();
  sessionStorage.clear();
});

describe('routes register view', () => {
  it('renders the register with every route domain on bare /routes', () => {
    const inventory = createDemoInventory();
    render(RoutePage, { props: { path: '/routes', inventory } });

    expect(screen.getByRole('heading', { name: /Routes/, level: 1 })).toBeInTheDocument();
    for (const item of inventory.routes) {
      expect(screen.getByText(item.domain)).toBeInTheDocument();
    }
    expect(screen.getByText('7 VISIBLE · 7 TOTAL')).toBeInTheDocument();
  });

  it('sorts broken routes before resolved routes', () => {
    const inventory = createDemoInventory();
    render(RoutePage, { props: { path: '/routes', inventory } });

    const rows = screen.getAllByRole('link', { name: /^Open route / });
    expect(rows[0]).toHaveAttribute('aria-label', 'Open route old.lab.example');
  });

  it('narrows the register rows with the filter input', async () => {
    const inventory = createDemoInventory();
    render(RoutePage, { props: { path: '/routes', inventory } });

    await fireEvent.input(screen.getByLabelText('Filter routes'), { target: { value: 'photos' } });

    expect(screen.getByText('photos.lab.example')).toBeInTheDocument();
    expect(screen.queryByText('watch.lab.example')).not.toBeInTheDocument();
    expect(screen.getByText('1 VISIBLE · 7 TOTAL')).toBeInTheDocument();
  });

  it('still renders the record detail view for /routes/:id', () => {
    const inventory = createDemoInventory();
    render(RoutePage, { props: { path: '/routes/6', inventory } });

    expect(screen.getByText('Routes · Record detail')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'auth.lab.example', level: 1 })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'All routes' })).toBeInTheDocument();
  });

  it('renders the empty state when no routes are observed', () => {
    const inventory = createDemoInventory();
    inventory.routes = [];
    render(RoutePage, { props: { path: '/routes', inventory } });

    expect(screen.getByText('NO ROUTES OBSERVED')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add source' })).toBeInTheDocument();
  });
});
