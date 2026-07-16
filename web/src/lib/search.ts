import type { Inventory } from './api';

export type SearchResult = {
  group: 'Services' | 'Routes' | 'Ports' | 'Hosts';
  title: string;
  meta: string;
  reason: string;
  state: string;
  href?: string;
};

export type SearchGroup = { label: SearchResult['group']; results: SearchResult[] };

export function buildSearchGroups(inventory: Inventory, rawQuery: string): SearchGroup[] {
  const query = rawQuery.trim().toLowerCase();
  if (!query) return [];

  const services = inventory.services.flatMap<SearchResult>((service) => {
    const name = service.name.toLowerCase();
    const stack = service.stack.toLowerCase();
    if (!name.includes(query) && stack !== query) return [];
    const nameMatch = name === query || (name.startsWith(`${query}-`) && !name.endsWith('-db'));
    return [{
      group: 'Services',
      title: service.name,
      meta: `SRV-${String(service.id).padStart(3, '0')} · ${service.stack} · ${service.host}`,
      reason: nameMatch ? `Name starts with ‘${query}’` : `Stack equals ‘${query}’`,
      state: service.state
    }];
  });

  const matchingServiceNames = new Set(
    inventory.services
      .filter((service) => service.name.toLowerCase().includes(query) || service.stack.toLowerCase() === query)
      .map((service) => service.name)
  );
  const directServiceNames = new Set(
    inventory.services
      .filter((service) => {
        const name = service.name.toLowerCase();
        return name === query || (name.startsWith(`${query}-`) && !name.endsWith('-db'));
      })
      .map((service) => service.name)
  );

  const routes = inventory.routes.flatMap<SearchResult>((route) => {
    const connected = [...directServiceNames].find((name) => route.service === name || route.upstream_host === name);
    if (!route.domain.toLowerCase().includes(query) && !connected) return [];
    return [{
      group: 'Routes',
      title: route.domain,
      meta: `RTE-${String(route.id).padStart(3, '0')} · ${route.proxy ?? 'Observed proxy'}`,
      reason: connected ? `Connected to ${connected}` : `Public name contains ‘${query}’`,
      state: route.status === 'broken' ? 'broken' : 'resolved',
      href: `/routes/${encodeURIComponent(route.domain)}`
    }];
  });

  const ports = inventory.ports.flatMap<SearchResult>((port) => {
    const service = port.service ?? '';
    if (!directServiceNames.has(service) && String(port.number) !== query) return [];
    return [{
      group: 'Ports',
      title: `${port.number} / ${port.protocol.replace(' / ', '+')}`,
      meta: `PRT-${String(port.id).padStart(3, '0')} · ${port.host ?? 'unknown host'} · ${port.published ? 'published' : 'internal'}`,
      reason: directServiceNames.has(service) ? `Declared by ${service}` : `Port number equals ‘${query}’`,
      state: 'allocated',
      href: '/ports'
    }];
  });

  const hosts = inventory.hosts.flatMap<SearchResult>((host) => {
    const count = inventory.services.filter((service) => service.host === host.name && matchingServiceNames.has(service.name)).length;
    if (!host.name.toLowerCase().includes(query) && count === 0) return [];
    return [{
      group: 'Hosts',
      title: host.name,
      meta: `HST-${String(host.id).padStart(3, '0')} · ${host.address}`,
      reason: count > 0 ? `Hosts ${count} matching record${count === 1 ? '' : 's'}` : `Host name contains ‘${query}’`,
      state: 'observed',
      href: `/hosts/${host.id}`
    }];
  });

  return [
    { label: 'Services', results: services },
    { label: 'Routes', results: routes },
    { label: 'Ports', results: ports },
    { label: 'Hosts', results: hosts }
  ].filter((group) => group.results.length > 0) as SearchGroup[];
}
