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

  const classifiedServices = inventory.services.map((service) => {
    const name = service.name.toLowerCase();
    const stack = service.stack.toLowerCase();
    return {
      service,
      matches: name.includes(query) || stack === query,
      direct: name === query || (name.startsWith(`${query}-`) && !name.endsWith('-db'))
    };
  });
  const matchingServices = classifiedServices.filter(({ matches }) => matches);
  const matchingServiceNames = new Set(matchingServices.map(({ service }) => service.name));
  const directServiceNames = new Set(classifiedServices.filter(({ service, direct }) => direct && matchingServiceNames.has(service.name)).map(({ service }) => service.name));
  const matchingServiceCountsByHost = new Map<string, number>();
  for (const { service } of matchingServices) {
    matchingServiceCountsByHost.set(service.host, (matchingServiceCountsByHost.get(service.host) ?? 0) + 1);
  }
  const services = matchingServices.map<SearchResult>(({ service, direct }) => {
    return {
      group: 'Services',
      title: service.name,
      meta: `SRV-${String(service.id).padStart(3, '0')} · ${service.stack} · ${service.host}`,
      reason: direct ? `Name starts with ‘${query}’` : `Stack equals ‘${query}’`,
      state: service.state
    };
  });

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
    const count = matchingServiceCountsByHost.get(host.name) ?? 0;
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
