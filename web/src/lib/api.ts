import * as demo from './demo';
import type { Change, Connector, Expiry, Host, Port, Route, Service } from './types';

type ListResponse<T> = { items: T[]; total: number };
export type Inventory = { services: Service[]; hosts: Host[]; ports: Port[]; routes: Route[]; changes: Change[]; expiries: Expiry[]; connectors: Connector[]; source: 'api' | 'demo'; error?: string };

export async function getSetupStatus(): Promise<{ configured: boolean; auth_disabled: boolean }> {
  const response = await fetch('/api/setup/status', { headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`Setup status returned ${response.status}.`);
  return response.json();
}

export async function login(password: string): Promise<void> {
  const response = await fetch('/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password }) });
  if (!response.ok) throw new Error(response.status === 429 ? 'Too many attempts. Wait a minute and try again.' : 'The password was not accepted.');
  const session = await response.json();
  if (session.csrf) sessionStorage.setItem('homedex-csrf', session.csrf);
}

async function list<T>(path: string): Promise<T[]> {
  const response = await fetch(`/api/${path}?limit=500`, { headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(response.status === 401 ? 'Sign in is required to load this inventory.' : `The ${path} API returned ${response.status}.`);
  return ((await response.json()) as ListResponse<T>).items ?? [];
}

export async function loadInventory(options: { demoOnEmpty?: boolean } = {}): Promise<Inventory> {
  try {
    const [rawServices, rawHosts, rawPorts, rawRoutes, rawChanges, certs, domains, connectors] = await Promise.all([
      list<Service>('services'), list<Host>('hosts'), list<Port>('ports'), list<Route>('routes'), list<Change>('changes'),
      list<Record<string, unknown>>('certs'), list<Record<string, unknown>>('domains'), list<Connector>('connectors')
    ]);
    const demoOnEmpty = options.demoOnEmpty ?? import.meta.env.DEV;
    if (demoOnEmpty && !rawServices.length && !rawHosts.length && !rawPorts.length && !rawRoutes.length && !connectors.length) {
      return { services: demo.services, hosts: demo.hosts, ports: demo.ports, routes: demo.routes, changes: demo.changes, expiries:demo.expiries, connectors:demo.connectors, source: 'demo' };
    }
    const hostById = new Map(rawHosts.map(h => [h.id, h.name]));
    const serviceById = new Map(rawServices.map(s => [s.id, s.name]));
    return {
      services: rawServices.map(s => ({ ...s, host: hostById.get(s.host_id ?? 0) ?? 'Unknown host', ports: 'Discovered', route: '—', stack: s.stack || 'standalone', last_seen: s.last_seen || 'just now' })),
      hosts: rawHosts,
      ports: rawPorts.map(p => ({ ...p, published: Boolean(p.published), host: hostById.get(p.host_id), service: serviceById.get(p.service_id) })),
      routes: rawRoutes.map(r => ({ ...r, tls: Boolean(r.tls), proxy: 'Discovered proxy', service: r.status === 'broken' ? 'No service found' : r.upstream_host })),
      changes: rawChanges,
      expiries: [
        ...certs.map((cert,index)=>toExpiry(cert,index,'TLS certificate')),
        ...domains.map((domain,index)=>toExpiry(domain,certs.length+index,'Domain'))
      ].sort((a,b)=>(a.days??Number.MAX_SAFE_INTEGER)-(b.days??Number.MAX_SAFE_INTEGER)),
      connectors,
      source: 'api'
    };
  } catch (error) {
    return { services: demo.services, hosts: demo.hosts, ports: demo.ports, routes: demo.routes, changes: demo.changes, expiries:demo.expiries, connectors:demo.connectors, source: 'demo', error: error instanceof Error ? error.message : 'Inventory could not be loaded.' };
  }
}

function toExpiry(item:Record<string,unknown>, index:number, type:string):Expiry {
  const rawDate=String(item.not_after??item.expires_at??'');
  const date=rawDate?new Date(rawDate):null;
  const days=date&&Number.isFinite(date.getTime())?Math.ceil((date.getTime()-Date.now())/86_400_000):null;
  return { id:Number(item.id??index+1),name:String(item.subject??item.name??'Unknown'),type,authority:String(item.issuer??item.registrar??item.source??'Unknown'),expires:date?date.toLocaleDateString(undefined,{month:'short',day:'numeric',year:'numeric'}):'Unknown',days,status:days===null?'Unknown':days<=14?'Action needed':days<=30?'Renew soon':'Healthy',checked:String(item.last_checked??'Recently') };
}
