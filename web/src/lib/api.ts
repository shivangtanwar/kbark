// Types mirror the JSON the Go server emits. Everything that originates
// in a cluster (names, messages, logs, diffs) is untrusted text; React
// escapes it, and nothing here is ever rendered as HTML.

export type Role = 'viewer' | 'approver' | 'admin';
export interface User { username: string; name: string; role: Role }
export interface Me { authEnabled: boolean; user: User | null }

export interface Ref { kind: string; name: string }
export interface Evidence { label: string; value: string }
export interface Action {
  type: string; targetKind: string; targetName: string;
  params?: Record<string, string>;
  title: string; rationale: string; risk: 'low' | 'medium'; diff: string;
  warnings?: string[]; reversible: boolean;
}
export interface HistoryEntry { time: string; state: IncidentState; actor: string; note?: string }
export type IncidentState =
  | 'detected' | 'proposed' | 'manual' | 'rejected' | 'applying'
  | 'verified' | 'failed' | 'rolled-back' | 'resolved';

export interface Incident {
  id: string; cluster: string; namespace: string; workload: Ref; key: string;
  family: string; reason: string; cause?: string; summary: string;
  container?: string; init?: boolean; probe?: string;
  evidence: Evidence[]; pods: string[];
  state: IncidentState; action?: Action; manualReason?: string; canRollback: boolean;
  explanation?: string; explanationState: 'pending' | 'ready' | 'unavailable' | 'error'; explanationNote?: string;
  openedAt: string; updatedAt: string; closedAt?: string; lastSeen: string;
  history: HistoryEntry[];
}

export interface ClusterSummary {
  name: string; state: string; error?: string;
  nodes: number; nodesReady: number; cordoned: number; namespaces: number;
  pods: number; healthy: number; failing: number; pending: number;
}
export interface PodRow {
  cluster: string; namespace: string; name: string; status: string; ready: string;
  restarts: number; created: string; node: string; owner: Ref;
  health: 'healthy' | 'failing' | 'pending' | 'done'; families?: string[];
}
export interface Info { version: string; ai: string; auditPath: string; statePath: string; publicURL: string }
export interface State { info: Info; now: string; clusters: ClusterSummary[]; pods: PodRow[]; incidents: Incident[] }

export interface AuditEntry {
  time: string; incident: string; cluster: string; namespace: string; workload: string;
  family: string; event: string; actor: string; action?: string; target?: string; diff?: string; note?: string;
}
export interface Sample { t: string; cluster: string; healthy: number; failing: number; pending: number; open: number }

export interface PolicyRule { name?: string; clusters?: string[]; namespaces?: string[]; mode?: string; actions?: string[]; memory_ceiling?: string }
export interface Policy { defaults: { memory_ceiling: string; max_attempts: number; attempt_window: string }; rules: PolicyRule[] | null; source: string }
export interface NotifyStatus { enabled: boolean; host?: string; events?: string[]; sent: number; failed: number; dropped: number; lastError?: string }
export interface Settings { info: Info; policy: Policy; notify: NotifyStatus; authEnabled: boolean; users?: User[]; clusters: string[] }

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: 'same-origin', ...init });
  let body: unknown = null;
  try { body = await res.json(); } catch { /* empty or non-JSON body */ }
  if (!res.ok) {
    const msg = (body as { error?: string } | null)?.error ?? res.statusText;
    throw new ApiError(res.status, msg.charAt(0).toUpperCase() + msg.slice(1));
  }
  return body as T;
}
const post = <T,>(path: string, data: unknown) =>
  request<T>(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) });

export const api = {
  me: () => request<Me>('/api/me'),
  login: (username: string, password: string) => post<Me>('/api/login', { username, password }),
  logout: () => post<Me>('/api/logout', {}),
  state: () => request<State>('/api/state'),
  audit: () => request<{ path: string; entries: AuditEntry[] }>('/api/audit'),
  history: (window: string) => request<{ window: string; samples: Sample[] }>(`/api/history?window=${window}`),
  settings: () => request<Settings>('/api/settings'),
  act: (id: string, verb: 'approve' | 'reject' | 'rollback' | 'replan', data: { operator?: string; reason?: string }) =>
    post<Incident>(`/api/incidents/${encodeURIComponent(id)}/${verb}`, data),
};
