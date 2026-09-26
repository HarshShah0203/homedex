import type { Host } from './types';

// Host kinds a hypervisor reports, named the way its own UI names them.
const hypervisorKinds: Record<string, string> = {
  vm: 'VM',
  lxc: 'Container',
  'proxmox-node': 'Proxmox node'
};

export function isHypervisorView(host: Host): boolean {
  return host.kind in hypervisorKinds;
}

export function hypervisorKind(host: Host): string {
  return hypervisorKinds[host.kind] ?? '';
}

// placement says what a hypervisor host is, where it runs and its power state,
// such as "VM on pve-01 · running". Hosts that report none of these, which is
// every Docker, SSH, Tailscale and manual host, get "".
export function placement(host: Host): string {
  const kind = hypervisorKind(host);
  const where = host.parent ? `on ${host.parent}` : '';
  const what = [kind, where].filter(Boolean).join(' ');
  return [what, host.power_state ?? ''].filter(Boolean).join(' · ');
}
