import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, ApiError, type AuditEntry, type Incident, type Me, type State } from './api';

export interface Toast { id: number; text: string; error?: boolean }

interface Live {
  me: Me | null;
  state: State | null;
  audit: { path: string; entries: AuditEntry[] };
  connected: boolean;
  now: number;
  cluster: string;
  setCluster: (c: string) => void;
  operator: string;
  setOperator: (s: string) => void;
  /** Name recorded against a change: the signed-in user, or the typed name in open mode. */
  signer: string;
  canApprove: boolean;
  theme: 'dark' | 'light';
  toggleTheme: () => void;
  toasts: Toast[];
  toast: (text: string, error?: boolean) => void;
  act: (id: string, verb: 'approve' | 'reject' | 'rollback' | 'replan', reason?: string) => Promise<Incident | null>;
  signIn: (username: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
  refresh: () => Promise<void>;
}

const Ctx = createContext<Live | null>(null);
export const useLive = () => {
  const v = useContext(Ctx);
  if (!v) throw new Error('useLive outside LiveProvider');
  return v;
};

const DONE: Record<string, string> = {
  approve: 'Approved and applied. Verifying.',
  reject: 'Rejected. Nothing was changed.',
  rollback: 'Rolled back.',
  replan: 'Planned again.',
};

export function LiveProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null);
  const [state, setState] = useState<State | null>(null);
  const [audit, setAudit] = useState<{ path: string; entries: AuditEntry[] }>({ path: '', entries: [] });
  const [connected, setConnected] = useState(false);
  const [now, setNow] = useState(Date.now());
  const [cluster, setCluster] = useState('');
  const [operator, setOperatorState] = useState(() => localStorage.getItem('kbark.operator') ?? '');
  const [theme, setTheme] = useState<'dark' | 'light'>(() => (localStorage.getItem('kbark.theme') === 'light' ? 'light' : 'dark'));
  const [toasts, setToasts] = useState<Toast[]>([]);
  const seq = useRef(0);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('kbark.theme', theme);
  }, [theme]);

  const toast = useCallback((text: string, error?: boolean) => {
    const id = ++seq.current;
    setToasts((t) => [...t.slice(-2), { id, text, error }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), error ? 8000 : 3500);
  }, []);

  const signedOut = !!me?.authEnabled && !me.user;

  const refresh = useCallback(async () => {
    try {
      const [s, a] = await Promise.all([api.state(), api.audit()]);
      setState(s);
      setAudit(a);
      setNow(new Date(s.now).getTime());
      setConnected(true);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setMe((m) => (m ? { ...m, user: null } : m));
      else setConnected(false);
    }
  }, []);

  useEffect(() => { api.me().then(setMe).catch(() => setMe({ authEnabled: false, user: null })); }, []);

  useEffect(() => {
    if (!me || signedOut) return;
    refresh();
    const es = new EventSource('/api/stream');
    es.addEventListener('change', () => { refresh(); });
    es.onerror = () => setConnected(false);
    const tick = setInterval(() => setNow((n) => n + 5000), 5000);
    return () => { es.close(); clearInterval(tick); };
  }, [me, signedOut, refresh]);

  const signer = me?.authEnabled ? (me.user?.name ?? '') : operator.trim();
  const canApprove = me?.authEnabled ? me.user?.role === 'approver' || me.user?.role === 'admin' : true;

  const act = useCallback<Live['act']>(async (id, verb, reason) => {
    try {
      const inc = await api.act(id, verb, { operator: signer, reason });
      toast(DONE[verb]);
      await refresh();
      return inc;
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Request failed', true);
      await refresh();
      return null;
    }
  }, [signer, toast, refresh]);

  const value = useMemo<Live>(() => ({
    me, state, audit, connected, now, cluster, setCluster, signer, canApprove,
    operator, setOperator: (s) => { setOperatorState(s); localStorage.setItem('kbark.operator', s.trim()); },
    theme, toggleTheme: () => setTheme((t) => (t === 'dark' ? 'light' : 'dark')),
    toasts, toast, act, refresh,
    signIn: async (u, p) => { setMe(await api.login(u, p)); },
    signOut: async () => { await api.logout().catch(() => undefined); setState(null); setMe((m) => (m ? { ...m, user: null } : m)); },
  }), [me, state, audit, connected, now, cluster, signer, canApprove, operator, theme, toasts, toast, act, refresh]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

/** Applies the header's cluster filter to anything with a `cluster` field. */
export function useScoped<T extends { cluster: string }>(items: T[] | undefined): T[] {
  const { cluster } = useLive();
  return useMemo(() => (items ?? []).filter((x) => !cluster || x.cluster === cluster), [items, cluster]);
}
