import { createDemoInventory } from './demo';
import type { Change, Connector, ConnectorInput, ConnectorMutation, ConnectorTest, ContextCounts, ContextExport, Expiry, Host, Inventory, InventoryIssue, InventoryIssueKind, InventoryResource, NotificationRule, NotificationRuleInput, NotificationTest, Port, Route, ScanRun, Service, Share, PortConflict, ManualEntityInput } from './types';

export type { Inventory } from './types';

type ListResponse<T> = { items: T[]; total: number };
const CONTEXT_LIMIT_BYTES = 100 * 1024;
const CORE_RESOURCES: InventoryResource[] = ['services', 'hosts', 'ports', 'routes', 'changes', 'expiry'];

class APIError extends Error {
  constructor(public resource: InventoryResource | string, public status: number, message: string) {
    super(message);
  }
}

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
  if (!response.ok) throw new APIError(path, response.status, response.status === 401 ? 'Sign in is required to load this inventory.' : response.status === 403 ? `The ${path} resource is not available in this read-only view.` : `The ${path} API returned ${response.status}.`);
  return ((await response.json()) as ListResponse<T>).items ?? [];
}

export async function loadInventory(options: { demoOnEmpty?: boolean } = {}): Promise<Inventory> {
  const requests = [
    list<Service>('services'), list<Host>('hosts'), list<Port>('ports'), list<Route>('routes'), list<Change>('changes'),
    list<Expiry>('expiry'), list<Connector>('connectors')
  ] as const;
  const results = await Promise.allSettled(requests);
  const issues: InventoryIssue[] = [];
  let readOnly = false;
  const values = results.map((result, index) => {
    if (result.status === 'fulfilled') return result.value;
    const resource = [...CORE_RESOURCES, 'connectors' as const][index];
    const issue = inventoryIssue(resource, result.reason);
    if (resource === 'connectors' && issue.status === 403) {
      readOnly = true;
    } else {
      issues.push(issue);
    }
    return [];
  });
  const [services, hosts, ports, routes, changes, expiries, connectors] = values as [Service[], Host[], Port[], Route[], Change[], Expiry[], Connector[]];
  const coreResults = results.slice(0, CORE_RESOURCES.length);
  const demoOnEmpty = options.demoOnEmpty ?? import.meta.env.DEV;
  const allCoreEmpty = [services, hosts, ports, routes, changes, expiries].every((items) => items.length === 0);
  const allCoreSucceeded = coreResults.every((result) => result.status === 'fulfilled');
  const allCoreFailed = coreResults.every((result) => result.status === 'rejected');
  const coreIssues = issues.filter((issue) => CORE_RESOURCES.includes(issue.resource));
  const allCoreOffline = allCoreFailed && coreIssues.length === CORE_RESOURCES.length && coreIssues.every((issue) => issue.kind === 'offline');
  if (demoOnEmpty && !readOnly && allCoreEmpty && (allCoreSucceeded || allCoreOffline)) {
    const error = allCoreFailed ? issues.map((issue) => issue.message).join(' ') : undefined;
    return createDemoInventory(error);
  }
  return { services, hosts, ports, routes, changes, expiries, connectors, source: 'api', readOnly, issues, ...(issues[0] ? { error: issues[0].message } : {}) };
}

export function createEmptyInventory(): Inventory {
  return { services: [], hosts: [], ports: [], routes: [], changes: [], expiries: [], connectors: [], source: 'api', readOnly: false, issues: [] };
}

export async function loadContextExport(): Promise<ContextExport> {
  const response = await fetch('/api/export/context?include_private=false', { headers: { Accept: 'text/markdown' } });
  if (!response.ok) throw new Error(`The context export API returned ${response.status}.`);

  const body = await response.arrayBuffer();
  const markdown = new TextDecoder().decode(body);
  const bytes = body.byteLength;
  const sha256 = await digest(body);

  return {
    markdown,
    bytes,
    size: formatBytes(bytes),
    filename: response.headers.get('Content-Disposition')?.match(/filename="?([^";]+)"?/i)?.[1] ?? 'homedex-context.md',
    title: markdown.match(/^#\s+(.+)$/m)?.[1]?.trim() ?? 'Homedex lab context',
    schema: markdown.match(/^Schema:\s*`([^`]+)`/m)?.[1] ?? 'unknown',
    counts: contextCounts(markdown),
    truncation: truncation(response.headers.get('X-Homedex-Truncation')),
    sha256,
    shortSha256: `${groupHex(sha256.slice(0, 12))} … ${sha256.slice(-4)}`
  };
}

export async function testConnector(input: ConnectorInput): Promise<ConnectorTest> {
  return requestJSON('/api/connectors/test', { method: 'POST', body: input });
}

export async function createConnector(input: ConnectorInput): Promise<ConnectorMutation> {
  // Connector creation persists the source before its first scan. The server
  // deliberately returns the persisted record with 502 when that scan fails,
  // so callers must retain the ID and offer a retry instead of creating a
  // duplicate source.
  return requestJSON('/api/connectors', { method: 'POST', body: input, acceptedStatuses: [502] });
}

export async function updateConnector(id: number, input: Partial<ConnectorInput>): Promise<ConnectorMutation> {
  return requestJSON(`/api/connectors/${id}`, { method: 'PATCH', body: input, acceptedStatuses: [502] });
}

export async function deleteConnector(id: number): Promise<void> {
  await requestJSON(`/api/connectors/${id}`, { method: 'DELETE' });
}

export async function testSavedConnector(id: number): Promise<ConnectorTest> {
  return requestJSON(`/api/connectors/${id}/test`, { method: 'POST' });
}

export async function scanConnector(id: number): Promise<{ status: string; scan_run_id: number; changes: number }> {
  return requestJSON(`/api/connectors/${id}/scan`, { method: 'POST' });
}

export async function loadConnectorScans(id: number): Promise<ScanRun[]> {
  const response = await requestJSON<ListResponse<Omit<ScanRun, 'stats'> & { stats: Record<string, number> | string }>>(`/api/connectors/${id}/scans`);
  return (response.items ?? []).map((run) => ({ ...run, stats: typeof run.stats === 'string' ? parseStats(run.stats) : run.stats ?? {} }));
}

export async function reviewChange(id: number, seen: boolean, note?: string): Promise<void> {
  await requestJSON(`/api/changes/${id}`, { method: 'PATCH', body: { seen, ...(note === undefined ? {} : { note }) } });
}

export async function reviewChanges(ids: number[], seen: boolean): Promise<void> {
  await requestJSON('/api/changes', { method: 'PATCH', body: { ids, seen } });
}

export async function loadNotificationRules(): Promise<NotificationRule[]> {
  const response = await requestJSON<ListResponse<NotificationRule>>('/api/notify/rules');
  return response.items ?? [];
}

export async function createNotificationRule(input: NotificationRuleInput): Promise<NotificationRule> {
  return requestJSON('/api/notify/rules', { method: 'POST', body: input });
}

export async function deleteNotificationRule(id: number): Promise<void> {
  await requestJSON(`/api/notify/rules/${id}`, { method: 'DELETE' });
}

export async function testNotificationRule(id: number): Promise<NotificationTest> {
  // The server reports a failed delivery with 502 and a structured
  // {status:"error", error} body, so accept it instead of throwing.
  return requestJSON(`/api/notify/rules/${id}/test`, { method: 'POST', acceptedStatuses: [502] });
}

export async function loadNextFreePort(hostID: number, start = 1024, end = 65535, protocol = 'tcp'): Promise<number> {
  const params = new URLSearchParams({ host_id: String(hostID), start: String(start), end: String(end), protocol });
  const response = await requestJSON<{ port: number }>(`/api/ports/next-free?${params}`);
  return response.port;
}

export async function loadShares(): Promise<Share[]> {
  const response = await requestJSON<ListResponse<Share>>('/api/share');
  return response.items ?? [];
}

export async function createShare(input: { name: string; expires_in_hours?: number }): Promise<Share> {
  return requestJSON('/api/share', { method: 'POST', body: input });
}

export async function revokeShare(id: number): Promise<void> {
  await requestJSON(`/api/share/${id}`, { method: 'DELETE' });
}

export async function loadPortConflicts(): Promise<PortConflict[]> {
  const response = await requestJSON<ListResponse<PortConflict>>('/api/ports/conflicts');
  return response.items ?? [];
}

export async function createManualEntity(input: ManualEntityInput): Promise<unknown> {
  return requestJSON('/api/entities', { method: 'POST', body: input });
}

export async function patchEntity(type: string, id: number, patch: Record<string, unknown>): Promise<unknown> {
  return requestJSON(`/api/entities/${type}/${id}`, { method: 'PATCH', body: patch });
}

export async function downloadExport(format: 'markdown' | 'json' | 'csv', view?: string): Promise<void> {
  const path = `/api/export/${format}${view ? `?view=${encodeURIComponent(view)}` : ''}`;
  const response = await fetch(path);
  if (!response.ok) throw new Error(`The export API returned ${response.status}.`);
  const blob = await response.blob();
  const fallback = `homedex-export.${format === 'markdown' ? 'md' : format}`;
  const name = response.headers.get('Content-Disposition')?.match(/filename="?([^";]+)"?/i)?.[1] ?? fallback;
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  URL.revokeObjectURL(url);
}

type RequestOptions = { method?: string; body?: unknown; acceptedStatuses?: number[] };

async function requestJSON<T = void>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET';
  const headers: Record<string, string> = { Accept: 'application/json' };
  const csrf = typeof sessionStorage === 'undefined' ? '' : sessionStorage.getItem('homedex-csrf') ?? '';
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrf) headers['X-Homedex-CSRF'] = csrf;
  const response = await fetch(path, { method, headers, ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }) });
  if (!response.ok && !options.acceptedStatuses?.includes(response.status)) {
    const raw = (await response.text()).trim();
    let message = raw || `The request returned ${response.status}.`;
    try {
      const parsed = JSON.parse(raw) as { error?: string; message?: string };
      message = parsed.error || parsed.message || message;
    } catch {
      // Non-JSON body: keep the raw text.
    }
    throw new APIError(path, response.status, message);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

function inventoryIssue(resource: InventoryResource, reason: unknown): InventoryIssue {
  if (reason instanceof APIError) return { resource, status: reason.status, kind: issueKind(reason.status), message: reason.message };
  const message = reason instanceof Error ? reason.message : 'The resource could not be loaded.';
  return { resource, status: 0, kind: 'offline', message };
}

function issueKind(status: number): InventoryIssueKind {
  if (status === 401) return 'unauthorized';
  if (status === 403) return 'forbidden';
  if (status === 0) return 'offline';
  if (status >= 500) return 'server';
  return 'invalid';
}

function parseStats(value: string): Record<string, number> {
  try {
    return JSON.parse(value) as Record<string, number>;
  } catch {
    return {};
  }
}

function contextCounts(markdown: string): ContextCounts {
  return {
    services: tableRows(markdown, 'Services'),
    hosts: tableRows(markdown, 'Hosts'),
    routes: tableRows(markdown, 'Routes'),
    ports: tableRows(markdown, 'Ports'),
    expiry: tableRows(markdown, 'Expiry')
  };
}

function tableRows(markdown: string, heading: string): number {
  const section = markdown.split(/^## /m).find((part) => part.startsWith(`${heading}\n`));
  if (!section) return 0;
  const rows = section.split('\n').filter((line) => line.trim().startsWith('|'));
  return Math.max(0, rows.length - 2);
}

function truncation(value: string | null): Record<string, number> {
  if (!value) return {};
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    return Object.fromEntries(Object.entries(parsed).filter((entry): entry is [string, number] => typeof entry[1] === 'number'));
  } catch {
    return {};
  }
}

async function digest(body: ArrayBuffer): Promise<string> {
  const value = await crypto.subtle.digest('SHA-256', body);
  return [...new Uint8Array(value)].map((byte) => byte.toString(16).padStart(2, '0')).join('').toUpperCase();
}

function groupHex(value: string): string {
  return value.match(/.{1,4}/g)?.join(' ') ?? value;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kilobytes = bytes / 1024;
  return `${Number.isInteger(kilobytes) ? kilobytes : kilobytes.toFixed(1)} KB`;
}

export const contextLimit = formatBytes(CONTEXT_LIMIT_BYTES);
