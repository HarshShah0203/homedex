export type EntityType = 'service' | 'host' | 'route' | 'port' | 'expiry' | 'change';
export type Service = { id: number; name: string; state: string; host: string; host_id: number | null; stack: string; image: string; tag: string; ports: string; route: string; last_seen: string; uptime?: string };
export type Host = { id: number; name: string; kind: string; address: string; os: string; arch: string; state: string; services?: number; ports?: number; last_seen?: string };
export type Port = { id: number; service_id: number; service: string; host_id: number | null; host: string; number: number; protocol: string; published: boolean; host_ip: string; container_port: number; source: string };
export type Route = { id: number; proxy_id?: number | null; proxy: string; domain: string; path_prefix: string; upstream_host: string; upstream_port: number | null; resolved_service_id?: number | null; service: string; resolve_confidence: string; tls: boolean; status: string; state: string; cert_expires_at?: string | null };
export type Expiry = { entity_type: string; id: number; name: string; kind: string; type: string; authority: string; expires_at: string | null; expires: string | null; days_remaining: number | null; days: number | null; status: string; checked_at: string; source: string; state: string };
export type Change = { id: number; scan_run_id: number; entity_type: string; entity_id: number; change_kind: string; summary: string; diff: unknown; seen: boolean; note?: string; created_at: string; connector?: string; scan_started_at?: string; scan_finished_at?: string | null; scan_status?: string; detail?: string };
export type Connector = { id:number; kind:string; name:string; enabled:boolean; schedule_minutes:number; last_status:string; last_error:string; created_at:string; updated_at:string; endpoint?:string; found?:string };
export type DrawerEntity = { type: EntityType; title: string; subtitle?: string; data: Record<string, unknown> };
export type InventoryResource = 'services' | 'hosts' | 'ports' | 'routes' | 'changes' | 'expiry' | 'connectors';
export type InventoryIssueKind = 'unauthorized' | 'forbidden' | 'offline' | 'server' | 'invalid';
export type InventoryIssue = { resource: InventoryResource; status: number; kind: InventoryIssueKind; message: string };
export type Inventory = { services: Service[]; hosts: Host[]; ports: Port[]; routes: Route[]; changes: Change[]; expiries: Expiry[]; connectors: Connector[]; source: 'api' | 'demo'; readOnly: boolean; issues: InventoryIssue[]; error?: string };
export type ContextCounts = { services: number; hosts: number; routes: number; ports: number; expiry: number };
export type ContextExport = { markdown: string; bytes: number; size: string; filename: string; title: string; schema: string; counts: ContextCounts; truncation: Record<string, number>; sha256: string; shortSha256: string };
export type ConnectorConfig = Record<string, string | number | boolean | string[]>;
export type ConnectorInput = { name: string; kind: string; config: ConnectorConfig; enabled?: boolean; schedule_minutes?: number };
export type ConnectorMutation = { connector: Connector; scan_run_id: number; changes: number; scan_error: string };
export type ConnectorTest = { status: 'ok' | 'error'; kind?: string; duration_ms?: number; error?: string };
export type ScanEvent = { type: string; connector_id?: number; scan_run_id?: number; changes?: number; error?: string; phase?: string; message?: string; progress?: number; stats?: Record<string, number> };
export type ScanRun = { id: number; started_at: string; finished_at: string | null; status: string; error: string; stats: Record<string, number> };
export type NotificationRule = { id: number; name: string; kind: 'expiry' | 'change'; threshold_days: number | null; filters: Record<string, unknown>; channels: string[]; channel_count: number; enabled: boolean; created_at: string; updated_at: string };
export type NotificationRuleInput = { name: string; kind: string; threshold_days?: number | null; filters?: Record<string, unknown>; channels: string[]; enabled?: boolean };
export type NotificationTest = { status: 'ok' | 'error'; error?: string };

export type Share = {
  id: number;
  name: string;
  created_at: string;
  expires_at: string | null;
  active: boolean;
  token?: string;
  share_url?: string;
};

export type PortConflict = {
  host_id: number | null;
  number: number;
  protocol: string;
  count: number;
  service_ids: string[];
};

export type ManualEntityInput = {
  entity_type: 'host' | 'service' | 'expiry';
  name?: string;
  kind?: string;
  host_id?: number;
  address?: string;
  os?: string;
  arch?: string;
  stack?: string;
  image?: string;
  tag?: string;
  state?: string;
  subject?: string;
  endpoint?: string;
  expires_at?: string;
  authority?: string;
};
