import type { Change, Connector, Expiry, Host, Inventory, Port, Route, Service } from './types';

export const hosts: Host[] = [
  { id: 1, name: 'gateway', kind: 'docker', address: '10.0.10.5', os: 'Debian 13', arch: 'amd64', state: 'active', services: 6, ports: 11, last_seen: '2m ago' },
  { id: 2, name: 'nas-01', kind: 'docker', address: '10.0.20.10', os: 'Ubuntu 24.04', arch: 'amd64', state: 'active', services: 19, ports: 54, last_seen: '2m ago' },
  { id: 3, name: 'core-01', kind: 'docker', address: '10.0.10.8', os: 'Alpine 3.22', arch: 'arm64', state: 'active', services: 14, ports: 34, last_seen: '2m ago' }
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
  { id:1,kind:'docker',name:'Docker · nas-01',enabled:true,schedule_minutes:15,last_status:'connected',last_error:'',created_at:'2026-07-16T10:40:00Z',updated_at:'2026-07-16T10:42:00Z',endpoint:'socket-proxy · tcp://10.0.20.10:2375',found:'19 services · 54 ports' },
  { id:2,kind:'npm',name:'Nginx Proxy Manager',enabled:true,schedule_minutes:15,last_status:'connected',last_error:'',created_at:'2026-07-16T10:40:00Z',updated_at:'2026-07-16T10:42:00Z',endpoint:'https://proxy.lab.internal · dedicated read-only account',found:'16 routes · 8 certs' },
  { id:3,kind:'caddy',name:'Caddy · core-01',enabled:true,schedule_minutes:15,last_status:'error',last_error:'Connection refused: dial tcp 10.0.10.8:2019',created_at:'2026-07-16T10:40:00Z',updated_at:'2026-07-16T10:42:00Z',endpoint:'http://10.0.10.8:2019',found:'4 routes' }
];

export function createDemoInventory(error?: string): Inventory {
  return {
    services: services.map((item) => ({ ...item })),
    hosts: hosts.map((item) => ({ ...item })),
    ports: ports.map((item) => ({ ...item })),
    routes: routes.map((item) => ({ ...item })),
    changes: changes.map((item) => ({ ...item })),
    expiries: expiries.map((item) => ({ ...item })),
    connectors: connectors.map((item) => ({ ...item })),
    source: 'demo',
    readOnly: false,
    issues: [],
    ...(error ? { error } : {})
  };
}
