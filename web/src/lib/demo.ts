import { plural } from './time';
import type { Change, Connector, Expiry, Host, Inventory, NotificationRule, Port, Route, Service, Share } from './types';

export const hosts: Host[] = [
  { id: 1, name: 'gateway', kind: 'docker', address: '10.0.10.5', os: 'Debian 13', arch: 'amd64', state: 'active', last_seen: '2m ago' },
  { id: 2, name: 'nas-01', kind: 'docker', address: '10.0.20.10', os: 'Ubuntu 24.04', arch: 'amd64', state: 'active', last_seen: '2m ago' },
  { id: 3, name: 'core-01', kind: 'docker', address: '10.0.10.8', os: 'Alpine 3.22', arch: 'arm64', state: 'active', last_seen: '2m ago' }
];

export const services: Service[] = [
  { id: 1, name: 'immich-server', state: 'running', host: 'nas-01', host_id: 2, stack: 'immich', image: 'ghcr.io/immich-app/server', tag: 'v1.135.3', ports: '2283 → 2283', route: 'photos.lab.example', last_seen: '2m ago', uptime: 'healthy · 18d uptime' },
  { id: 2, name: 'jellyfin', state: 'running', host: 'nas-01', host_id: 2, stack: 'media', image: 'jellyfin/jellyfin', tag: '10.10.7', ports: '8096 → 8096', route: 'watch.lab.example', last_seen: '2m ago', uptime: 'healthy · 31d uptime' },
  { id: 3, name: 'npm-app', state: 'running', host: 'gateway', host_id: 1, stack: 'proxy', image: 'jc21/nginx-proxy-manager', tag: '2.12.3', ports: '80, 81, 443', route: '—', last_seen: '2m ago', uptime: 'healthy · 31d uptime' },
  { id: 4, name: 'pihole', state: 'running', host: 'core-01', host_id: 3, stack: 'network', image: 'pihole/pihole', tag: '2026.05', ports: '53/tcp, 53/udp', route: 'dns.lab.example', last_seen: '2m ago', uptime: 'healthy · 9d uptime' },
  { id: 5, name: 'paperless-web', state: 'running', host: 'nas-01', host_id: 2, stack: 'paperless', image: 'ghcr.io/paperless-ngx', tag: '2.17.1', ports: '8000', route: 'docs.lab.example', last_seen: '2m ago', uptime: 'healthy · 12d uptime' },
  { id: 6, name: 'grafana', state: 'running', host: 'core-01', host_id: 3, stack: 'observability', image: 'grafana/grafana', tag: '12.0.2', ports: '3000', route: 'metrics.lab.example', last_seen: '2m ago', uptime: 'healthy · 5d uptime' },
  { id: 7, name: 'authelia', state: 'restarting', host: 'gateway', host_id: 1, stack: 'identity', image: 'authelia/authelia', tag: '4.38.18', ports: '9091', route: 'auth.lab.example', last_seen: '2m ago', uptime: 'restarting · 4m' },
  { id: 8, name: 'whoami-dev', state: 'gone', host: 'devbox', host_id: null, stack: 'sandbox', image: 'traefik/whoami', tag: 'v1.10', ports: '8080', route: '—', last_seen: '2d ago', uptime: 'last seen 2d ago' },
  { id: 9, name: 'immich-db', state: 'running', host: 'nas-01', host_id: 2, stack: 'immich', image: 'tensorchord/pgvecto-rs', tag: 'pg14-v0.2.0', ports: '5432', route: '—', last_seen: '2m ago', uptime: 'healthy · 18d uptime' },
  { id: 10, name: 'paperless-redis', state: 'running', host: 'nas-01', host_id: 2, stack: 'paperless', image: 'redis', tag: '7.4-alpine', ports: '6379', route: '—', last_seen: '2m ago', uptime: 'healthy · 12d uptime' },
  { id: 11, name: 'traefik', state: 'running', host: 'gateway', host_id: 1, stack: 'proxy', image: 'traefik', tag: 'v3.4', ports: '8080', route: '—', last_seen: '2m ago', uptime: 'healthy · 31d uptime' }
];

export const routes: Route[] = [
  ['photos.lab.example','Nginx Proxy Manager','immich-server','nas-01',2283,'ok','high'],
  ['watch.lab.example','Nginx Proxy Manager','jellyfin','nas-01',8096,'ok','high'],
  ['docs.lab.example','Traefik','paperless-web','nas-01',8000,'ok','high'],
  ['metrics.lab.example','Traefik','grafana','core-01',3000,'ok','high'],
  ['dns.lab.example','Caddy','pihole','core-01',80,'ok','high'],
  ['auth.lab.example','Nginx Proxy Manager','authelia','gateway',9091,'ok','high'],
  ['old.lab.example','Traefik','No service found','10.0.20.14',8080,'broken','none']
].map((r, i) => ({ id:i+1, proxy:r[1] as string, domain:r[0] as string, service:r[2] as string, upstream_host:r[3] as string, upstream_port:r[4] as number, resolved_service_id:services.find((service) => service.name === r[2])?.id ?? null, cert_expires_at:null, status:r[5] as string, resolve_confidence:r[6] as string, tls:true, state:'active', path_prefix:'' }));

const portRows: Array<[number,string,string,string,boolean]> = [[53,'tcp / udp','pihole','core-01',true],[80,'tcp','npm-app','gateway',true],[80,'tcp','caddy','core-01',true],[81,'tcp','npm-app','gateway',true],[443,'tcp','npm-app','gateway',true],[443,'tcp','caddy','core-01',true],[3000,'tcp','grafana','core-01',true],[5432,'tcp','immich-db','nas-01',false],[6379,'tcp','paperless-redis','nas-01',false],[8000,'tcp','paperless-web','nas-01',false],[8080,'tcp','traefik','gateway',true],[8080,'tcp','traefik','core-01',true],[8096,'tcp','jellyfin','nas-01',true],[9091,'tcp','authelia','gateway',false],[2283,'tcp','immich-server','nas-01',false]];
export const ports: Port[] = portRows.map((p,i)=>({ id:i+1, number:p[0], protocol:p[1], service:p[2], host:p[3], published:p[4], service_id:i+1, host_id:hosts.find(h=>h.name===p[3])?.id ?? null, host_ip:'0.0.0.0', container_port:p[0], source:'docker' }));

export const expiries: Expiry[] = [
  ['cert',1,'auth.lab.example','TLS certificate','Let’s Encrypt R11','Jul 30, 2026',14,'action_needed','6h ago'],
  ['cert',2,'docs.lab.example','TLS certificate','Let’s Encrypt R11','Aug 8, 2026',23,'expiring','6h ago'],
  ['cert',3,'watch.lab.example','TLS certificate','Let’s Encrypt R10','Sep 18, 2026',64,'upcoming','6h ago'],
  ['cert',4,'photos.lab.example','TLS certificate','Let’s Encrypt R10','Oct 13, 2026',89,'upcoming','6h ago'],
  ['manual',5,'nas warranty','Manual','Synology','Nov 2, 2026',109,'upcoming','—'],
  ['domain',6,'lab.example','Domain','Cloudflare','Dec 18, 2026',155,'upcoming','1d ago']
].map((item) => ({ entity_type:item[0] as string,id:item[1] as number,name:item[2] as string,kind:item[3] as string,type:item[3] as string,authority:item[4] as string,expires_at:item[5] as string,expires:item[5] as string,days_remaining:item[6] as number,days:item[6] as number,status:item[7] as string,checked_at:item[8] as string,source:'demo',state:'active' }));

export const changes: Change[] = [
  { id:1,scan_run_id:13,entity_type:'service',entity_id:5,change_kind:'added',summary:'paperless-web was added',detail:'New container in paperless on nas-01',diff:'{}',seen:false,created_at:'Today, 10:42 AM' },
  { id:2,scan_run_id:13,entity_type:'service',entity_id:1,change_kind:'modified',summary:'immich-server image changed',detail:'Image tag updated on nas-01 · v1.134.0 → v1.135.3',diff:'{}',seen:false,created_at:'Today, 10:42 AM' },
  { id:3,scan_run_id:13,entity_type:'port',entity_id:12,change_kind:'modified',summary:'jellyfin published port changed',detail:'Port mapping updated · 8097:8096 → 8096:8096',diff:'{}',seen:false,created_at:'Today, 10:42 AM' },
  { id:4,scan_run_id:12,entity_type:'cert',entity_id:4,change_kind:'modified',summary:'photos.lab.example certificate renewed',detail:'Let’s Encrypt issued a new certificate · 21 days → 89 days',diff:'{}',seen:true,created_at:'Today, 6:00 AM' },
  { id:5,scan_run_id:11,entity_type:'service',entity_id:8,change_kind:'removed',summary:'whoami-dev is no longer present',detail:'Last seen on devbox at 4:18 PM',diff:'{}',seen:true,created_at:'Yesterday, 4:18 PM' }
];

export const connectors: Connector[] = [
  { id:1,kind:'docker',name:'Docker · nas-01',enabled:true,schedule_minutes:15,last_status:'connected',last_error:'',created_at:'2026-07-16T10:40:00Z',updated_at:'2026-07-16T10:42:00Z',endpoint:'socket-proxy · tcp://10.0.20.10:2375' },
  { id:2,kind:'npm',name:'Nginx Proxy Manager',enabled:true,schedule_minutes:15,last_status:'connected',last_error:'',created_at:'2026-07-16T10:40:00Z',updated_at:'2026-07-16T10:42:00Z',endpoint:'https://proxy.lab.internal · dedicated read-only account' },
  { id:3,kind:'caddy',name:'Caddy · core-01',enabled:true,schedule_minutes:15,last_status:'error',last_error:'Connection refused: dial tcp 10.0.10.8:2019',created_at:'2026-07-16T10:40:00Z',updated_at:'2026-07-16T10:42:00Z',endpoint:'http://10.0.10.8:2019' }
];

const DAY_MS = 86_400_000;

function isoFrom(now: Date, offsetMs: number): string {
  return new Date(now.getTime() + offsetMs).toISOString();
}

// What each fabricated source read. Its "Indexed" summary is counted from the
// same records the registers list, and hosts carry no fixed totals, so the
// Hosts cards, the host inspector and the Sources page agree with each other.
const connectorScope: Record<number, { host?: string; proxy?: string }> = {
  1: { host: 'nas-01' },
  2: { proxy: 'Nginx Proxy Manager' },
  3: { proxy: 'Caddy' }
};

type IndexedRecords = Pick<Inventory, 'services' | 'ports' | 'routes' | 'expiries'>;

function indexedSummary(connector: Connector, records: IndexedRecords): string | undefined {
  const scope = connectorScope[connector.id];
  if (scope?.host) {
    const serviceCount = records.services.filter((service) => service.host === scope.host).length;
    const portCount = records.ports.filter((port) => port.host === scope.host).length;
    return `${plural(serviceCount, 'service')} · ${plural(portCount, 'port')}`;
  }
  if (scope?.proxy) {
    const domains = records.routes.filter((route) => route.proxy === scope.proxy).map((route) => route.domain);
    const certCount = records.expiries.filter((record) => record.entity_type === 'cert' && domains.includes(record.name)).length;
    return certCount ? `${plural(domains.length, 'route')} · ${plural(certCount, 'cert')}` : plural(domains.length, 'route');
  }
  return undefined;
}

// Dates are anchored to `now` so the hosted live demo never shows a
// certificate "14 days out" that lapsed months ago, or a scan from last season.
export function createDemoInventory(error?: string, now: Date = new Date()): Inventory {
  const records: IndexedRecords = {
    services: services.map((item) => ({ ...item })),
    ports: ports.map((item) => ({ ...item })),
    routes: routes.map((item) => ({ ...item })),
    expiries: expiries.map((item) => {
      const at = item.days_remaining === null ? item.expires_at : isoFrom(now, item.days_remaining * DAY_MS);
      return { ...item, expires_at: at, expires: at };
    })
  };
  return {
    ...records,
    hosts: hosts.map((item) => ({ ...item })),
    changes: changes.map((item) => ({ ...item })),
    connectors: connectors.map((item) => ({ ...item, found: indexedSummary(item, records), created_at: isoFrom(now, -30 * DAY_MS), updated_at: isoFrom(now, -2 * 60_000) })),
    source: 'demo',
    readOnly: false,
    issues: [],
    ...(error ? { error } : {})
  };
}

// The hosted live demo also shows image update checks as a scanned lab would:
// two services whose tag now points at a newer build, every other running
// container checked and current. The dev-server fallback keeps the plain
// inventory, which is why this is applied separately.
const demoUpdates: Record<string, { running: string; latest: string }> = {
  jellyfin: {
    running: 'sha256:3f9c2e71a0b84d5e96c1f7a2b8d40e6c5a19f3b7e2d84c0a6b1e9f5d7c3a2b18',
    latest: 'sha256:8b41d07e5c2a93f6d1e0b7c4a5f28d9e6c3b10a7f4e2d95c8b1a6e3f0d7c4b29'
  },
  'immich-server': {
    running: 'sha256:c27e5b90d4a1f63e8b2c7d05a9f14e6b3d8c2a71f0e5b94d6c3a8e1f7b2d0c46',
    latest: 'sha256:1d6a8f3c0e5b72d9a4c1f8e6b3d20a7c5e9f14b8d2a6c3e0f7b5d19a8c4e2f63'
  }
};

export function withDemoUpdates(inventory: Inventory, now: Date = new Date()): Inventory {
  const checked = isoFrom(now, -3 * 3_600_000);
  return {
    ...inventory,
    services: inventory.services.map((service) => {
      if (service.state === 'gone') return service;
      const update = demoUpdates[service.name];
      return update
        ? { ...service, update_status: 'update_available', update_checked_at: checked, running_digest: update.running, latest_digest: update.latest }
        : { ...service, update_status: 'up_to_date', update_checked_at: checked };
    }),
    connectors: [
      ...inventory.connectors,
      { id: 4, kind: 'registry', name: 'Image updates', enabled: true, schedule_minutes: 1440, last_status: 'connected', last_error: '', created_at: isoFrom(now, -30 * DAY_MS), updated_at: checked, endpoint: 'Anonymous registry manifest checks', found: '2 updates · 10 images' }
    ]
  };
}

// The hosted live demo also shows a Proxmox VE source, as a lab with a
// hypervisor would: one node and two guests on it, each at the address its
// guest agent or container reported. Like the image update checks it is
// applied only to the live demo, so the dev-server fallback keeps the plain
// inventory.
export function withDemoProxmox(inventory: Inventory, now: Date = new Date()): Inventory {
  const node: Host = { id: 4, name: 'pve-01', kind: 'proxmox-node', address: '10.0.10.2', os: 'Proxmox VE', arch: '', state: 'active', last_seen: '2m ago', aliases: [], power_state: 'online', parent_id: null, parent: '' };
  const guests: Host[] = [
    { id: 5, name: 'home-assistant', kind: 'vm', address: '10.0.30.21', os: '', arch: '', state: 'active', last_seen: '2m ago', aliases: [], power_state: 'running', parent_id: node.id, parent: node.name },
    { id: 6, name: 'unifi', kind: 'lxc', address: '10.0.10.31', os: '', arch: '', state: 'active', last_seen: '2m ago', aliases: [], power_state: 'running', parent_id: node.id, parent: node.name }
  ];
  return {
    ...inventory,
    hosts: [...inventory.hosts, node, ...guests],
    connectors: [
      ...inventory.connectors,
      { id: 5, kind: 'proxmox', name: `Proxmox VE · ${node.name}`, enabled: true, schedule_minutes: 15, last_status: 'connected', last_error: '', created_at: isoFrom(now, -30 * DAY_MS), updated_at: isoFrom(now, -2 * 60_000), endpoint: `https://${node.name}.lab.internal:8006 · PVEAuditor token`, found: `${plural(1, 'node')} · ${plural(guests.length, 'guest')}` }
    ]
  };
}

export function createDemoNotificationRules(): NotificationRule[] {
  return [
    { id: 1, name: 'Expiry 14d', kind: 'expiry', threshold_days: 14, filters: {}, channels: ['ntfy'], channel_count: 1, enabled: true, created_at: '', updated_at: '' },
    { id: 2, name: 'Changes', kind: 'change', threshold_days: null, filters: { change_kinds: ['added', 'removed'] }, channels: ['discord'], channel_count: 1, enabled: true, created_at: '', updated_at: '' }
  ];
}

export function createDemoShares(now: Date = new Date()): Share[] {
  return [{ id: 1, name: 'Family wiki', created_at: isoFrom(now, -3 * DAY_MS), expires_at: isoFrom(now, 4 * DAY_MS), active: true }];
}
