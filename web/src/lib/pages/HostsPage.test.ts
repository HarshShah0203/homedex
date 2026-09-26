import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import { createDemoInventory } from '../demo';
import type { Host, Inventory } from '../types';
import HostsPage from './HostsPage.svelte';

afterEach(cleanup);

// The demo lab plus what a Proxmox source reports: a node and two guests on it.
function withHypervisor(): Inventory {
  const inventory = createDemoInventory();
  const node: Host = { id: 4, name: 'pve-01', kind: 'proxmox-node', address: '192.0.2.2', os: 'Proxmox VE', arch: '', state: 'active', power_state: 'online', parent_id: null, parent: '' };
  const guests: Host[] = [
    { id: 5, name: 'home-assistant', kind: 'vm', address: '192.0.2.21', os: '', arch: '', state: 'active', aliases: ['198.51.100.21'], power_state: 'running', parent_id: 4, parent: 'pve-01' },
    { id: 6, name: 'old-wiki', kind: 'lxc', address: '', os: '', arch: '', state: 'active', power_state: 'stopped', parent_id: 4, parent: 'pve-01' }
  ];
  return { ...inventory, hosts: [...inventory.hosts, node, ...guests] };
}

function card(name: string): HTMLElement {
  const element = document.querySelector<HTMLElement>(`[data-component-id="host-record-${name}"]`);
  if (!element) throw new Error(`no card for ${name}`);
  return element;
}

describe('HostsPage with a hypervisor source', () => {
  it('shows each guest on its node with its power state, and nothing extra for other hosts', () => {
    render(HostsPage, { props: { path: '/hosts', inventory: withHypervisor() } });

    expect(within(card('home-assistant')).getByText('VM on pve-01 · running')).toBeInTheDocument();
    expect(within(card('old-wiki')).getByText('Container on pve-01 · stopped')).toBeInTheDocument();
    expect(within(card('pve-01')).getByText('Proxmox node · online')).toBeInTheDocument();
    expect(card('nas-01').querySelector('.placement')).toBeNull();
  });

  it('inspects a guest with the node it runs on as a link, not Docker engine facts', () => {
    render(HostsPage, { props: { path: '/hosts/5', inventory: withHypervisor() } });

    const inspector = screen.getByRole('dialog', { name: 'Connected records for home-assistant' });
    expect(within(inspector).getByText('VM')).toBeInTheDocument();
    expect(within(inspector).getByText('198.51.100.21')).toBeInTheDocument();
    expect(within(inspector).getByRole('link', { name: 'pve-01' })).toHaveAttribute('href', '/hosts/4');
    expect(within(inspector).getByText('running')).toBeInTheDocument();
    expect(within(inspector).queryByText('Docker 28.1.1')).not.toBeInTheDocument();
  });

  it('says a stopped guest reports no address', () => {
    render(HostsPage, { props: { path: '/hosts/6', inventory: withHypervisor() } });
    const inspector = screen.getByRole('dialog', { name: 'Connected records for old-wiki' });
    expect(within(inspector).getByText('Not reported')).toBeInTheDocument();
    expect(within(inspector).getByText('stopped')).toBeInTheDocument();
  });

  it('keeps the Docker host inspector as it was', () => {
    render(HostsPage, { props: { path: '/hosts/2', inventory: withHypervisor() } });
    const inspector = screen.getByRole('dialog', { name: 'Connected records for nas-01' });
    expect(within(inspector).getByText('Docker 28.1.1')).toBeInTheDocument();
    expect(within(inspector).queryByText('Runs on')).not.toBeInTheDocument();
  });
});
