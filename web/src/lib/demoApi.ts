import type { Inventory, PortConflict } from './types';

// Answers for the read-only endpoints the live demo cannot call. They are
// derived from the same fabricated inventory the registers show, so every page
// of the demo agrees with every other page.

type Cell = string | number | boolean | null | undefined;

function table(heading: string, columns: string[], rows: Cell[][]): string {
  const clean = (cell: Cell) => String(cell ?? '').replaceAll('|', '\\|').replaceAll('\n', ' ');
  return [
    `## ${heading}`,
    '',
    `| ${columns.join(' | ')} |`,
    `|${columns.map(() => '---').join('|')}|`,
    ...rows.map((row) => `| ${row.map(clean).join(' | ')} |`),
    ''
  ].join('\n');
}

// Mirrors the section layout of the server's context export
// (internal/export: "Homedex lab context") so the Copy my lab page parses it
// with the same code as a real response.
export function demoContextMarkdown(inventory: Inventory): string {
  const sections = [
    table('Hosts', ['Name', 'Kind', 'Address', 'OS/arch', 'Notes'], inventory.hosts.map((host) => [host.name, host.kind, host.address, `${host.os} ${host.arch}`, ''])),
    table('Services', ['Host', 'Service', 'Stack', 'Image', 'State', 'Ports', 'Tags', 'Labels', 'Notes'], inventory.services.map((service) => [service.host, service.name, service.stack, `${service.image}:${service.tag}`, service.state, service.ports, '', '', ''])),
    table('Ports', ['Host', 'Service', 'Published', 'Container', 'Protocol', 'Scope'], inventory.ports.map((port) => [port.host, port.service, port.number, port.container_port, port.protocol, port.published ? 'published' : 'internal'])),
    table('Routes', ['Domain', 'Path', 'Proxy', 'Upstream', 'Service', 'TLS', 'Status'], inventory.routes.map((route) => [route.domain, route.path_prefix || '/', route.proxy, `${route.upstream_host}:${route.upstream_port ?? ''}`, route.service, route.tls, route.status])),
    table('Expiry', ['Name', 'Type', 'Authority', 'Expires', 'State'], inventory.expiries.map((record) => [record.name, record.kind, record.authority, record.expires_at, record.state]))
  ];
  const omitted = ['hosts', 'services', 'ports', 'routes', 'expiry'].map((name) => `- ${name}: 0 omitted`);
  return ['# Homedex lab context', '', 'Schema: `homedex.inventory.v1`', '', ...sections, '## Truncation report', '', ...omitted, ''].join('\n');
}

export function demoPortConflicts(inventory: Inventory): PortConflict[] {
  const groups = new Map<string, PortConflict>();
  for (const port of inventory.ports) {
    const key = `${port.host_id}:${port.number}:${port.protocol}`;
    const group = groups.get(key) ?? { host_id: port.host_id, number: port.number, protocol: port.protocol, count: 0, service_ids: [] };
    group.count += 1;
    group.service_ids.push(String(port.service_id));
    groups.set(key, group);
  }
  return [...groups.values()].filter((group) => group.count > 1);
}

export function demoNextFreePort(inventory: Inventory, hostID: number, start: number, end: number, protocol: string): number {
  const used = new Set(inventory.ports.filter((port) => port.host_id === hostID && port.protocol.includes(protocol)).map((port) => port.number));
  for (let port = start; port <= end; port += 1) if (!used.has(port)) return port;
  throw new Error(`No unused ${protocol} port between ${start} and ${end}.`);
}

export function demoExportFile(inventory: Inventory, format: 'markdown' | 'json' | 'csv', view?: string): { blob: Blob; name: string } {
  if (format === 'json') {
    const { services, hosts, ports, routes, expiries } = inventory;
    const body = JSON.stringify({ schema_version: 'homedex.inventory.v1', hosts, services, ports, routes, expiry: expiries }, null, 2);
    return { blob: new Blob([body], { type: 'application/json' }), name: 'homedex-export.json' };
  }
  if (format === 'csv') {
    const quote = (cell: Cell) => {
      const text = String(cell ?? '');
      return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
    };
    const rows = [['host', 'name', 'stack', 'image', 'tag', 'state', 'ports', 'route'], ...inventory.services.map((service) => [service.host, service.name, service.stack, service.image, service.tag, service.state, service.ports, service.route])];
    return { blob: new Blob([rows.map((row) => row.map(quote).join(',')).join('\n') + '\n'], { type: 'text/csv' }), name: `homedex-${view || 'services'}.csv` };
  }
  return { blob: new Blob([demoContextMarkdown(inventory)], { type: 'text/markdown' }), name: 'homedex-export.md' };
}
