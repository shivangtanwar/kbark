import clsx from 'clsx';
import { AlertTriangle, Boxes, FlaskConical, FileText, LayoutGrid, LogOut, Menu, Moon, Server, Settings as Cog, Sun, X } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { useLive } from '../lib/live';
import { groupOf } from '../lib/vocab';
import { ErrorBoundary } from './ErrorBoundary';
import { Input } from './ui';

function Logo() {
  return (
    <NavLink to="/" className="flex items-center gap-2.5 px-2 pb-5 pt-1" aria-label="kbark overview">
      <svg viewBox="0 0 28 28" className="size-7" aria-hidden="true">
        <rect width="28" height="28" rx="7" fill="var(--accent)" />
        <path d="M14 5.5l7.4 4.25v8.5L14 22.5l-7.4-4.25v-8.5z" fill="none" stroke="var(--accent-fg)" strokeWidth="1.6" strokeLinejoin="round" opacity=".5" />
        <path d="M10.2 14.2l2.6 2.6 5-5.4" fill="none" stroke="var(--accent-fg)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
      <span className="leading-tight">
        <span className="block text-base font-semibold tracking-tight">kbark</span>
        <span className="block text-[11px] font-medium text-fg-3">Autofix</span>
      </span>
    </NavLink>
  );
}

function Item({ to, icon, children, badge, end }: { to: string; icon: ReactNode; children: ReactNode; badge?: number; end?: boolean }) {
  return (
    <NavLink to={to} end={end}
      className={({ isActive }) => clsx('flex items-center gap-2.5 rounded-lg px-2.5 py-2 font-medium transition-colors [&_svg]:size-[17px] [&_svg]:shrink-0',
        isActive ? 'bg-accent-soft text-fg [&_svg]:text-accent' : 'text-fg-2 hover:bg-raise hover:text-fg')}>
      {icon}<span className="flex-1">{children}</span>
      {!!badge && <span className="num min-w-[18px] rounded-full bg-warn px-1.5 text-center text-[11px] font-semibold leading-[18px] text-black">{badge}</span>}
    </NavLink>
  );
}

const ROLE: Record<string, string> = { viewer: 'Viewer', approver: 'Approver', admin: 'Admin' };

export function Shell({ title, crumb }: { title: string; crumb?: ReactNode }) {
  const { state, me, connected, cluster, setCluster, operator, setOperator, theme, toggleTheme, signOut } = useLive();
  const [open, setOpen] = useState(false);
  const loc = useLocation();
  useEffect(() => { setOpen(false); window.scrollTo(0, 0); }, [loc.pathname]);
  useEffect(() => { document.title = `${title} · kbark`; }, [title]);

  const awaiting = (state?.incidents ?? []).filter((i) => !i.closedAt && groupOf(i) === 'awaiting').length;

  return (
    <div className="min-h-full md:grid md:grid-cols-[232px_minmax(0,1fr)]">
      {open && <div className="fixed inset-0 z-30 bg-black/50 md:hidden" onClick={() => setOpen(false)} />}
      <aside className={clsx('fixed inset-y-0 left-0 z-40 flex w-[232px] flex-col border-r border-line bg-side p-3 transition-transform md:sticky md:top-0 md:h-screen md:translate-x-0', open ? 'translate-x-0' : '-translate-x-full')}>
        <Logo />
        <nav className="flex flex-col gap-0.5" aria-label="Main">
          <Item to="/" end icon={<LayoutGrid />}>Overview</Item>
          <Item to="/incidents" icon={<AlertTriangle />} badge={awaiting}>Incidents</Item>
          <Item to="/clusters" icon={<Server />}>Clusters</Item>
          <Item to="/pods" icon={<Boxes />}>Pods</Item>
          <Item to="/audit" icon={<FileText />}>Audit log</Item>
          <Item to="/settings" icon={<Cog />}>Settings</Item>
          {state?.info.demo && <Item to="/demo" icon={<FlaskConical />}>Demo lab</Item>}
        </nav>

        <div className="mt-auto flex flex-col gap-3 border-t border-line px-1.5 pt-3">
          {me?.authEnabled && me.user ? (
            <div className="flex items-center gap-2.5">
              <span className="grid size-8 shrink-0 place-items-center rounded-full bg-accent-soft text-xs font-semibold text-accent">{me.user.name.split(/\s+/).map((w) => w[0]).slice(0, 2).join('').toUpperCase()}</span>
              <span className="min-w-0 flex-1 leading-tight">
                <span className="block truncate font-medium">{me.user.name}</span>
                <span className="block text-xs text-fg-3">{ROLE[me.user.role] ?? me.user.role}</span>
              </span>
              <button onClick={signOut} title="Sign out" aria-label="Sign out" className="grid size-8 place-items-center rounded-lg text-fg-2 hover:bg-raise hover:text-fg"><LogOut className="size-4" /></button>
            </div>
          ) : (
            <label className="flex flex-col gap-1.5">
              <span className="text-[11px] font-semibold uppercase tracking-[0.06em] text-fg-3">Signing as</span>
              <Input id="operator" value={operator} onChange={(e) => setOperator(e.target.value)} placeholder="Your name" autoComplete="name" maxLength={60} />
            </label>
          )}
          <div className="flex items-center justify-between text-xs text-fg-2">
            <span className="inline-flex items-center gap-2">
              <i className={clsx('size-[7px] rounded-full', connected ? 'bg-ok shadow-[0_0_0_3px_var(--ok-soft)]' : 'bg-bad shadow-[0_0_0_3px_var(--bad-soft)]')} />
              {connected ? 'Live' : 'Reconnecting'}
            </span>
            <button onClick={toggleTheme} title={theme === 'dark' ? 'Use light theme' : 'Use dark theme'} aria-label="Toggle theme" className="grid size-7 place-items-center rounded-lg hover:bg-raise hover:text-fg">
              {theme === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
            </button>
          </div>
          <div className="truncate text-[11.5px] text-fg-3" title={state?.info.version}>kbark {state?.info.version ?? ''}</div>
        </div>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="sticky top-0 z-20 flex items-end justify-between gap-4 border-b border-line bg-bg/90 px-4 pb-3.5 pt-4 backdrop-blur md:px-7">
          <div className="flex min-w-0 items-end gap-3">
            <button className="mb-0.5 grid size-8 place-items-center rounded-lg border border-line-2 md:hidden" onClick={() => setOpen(!open)} aria-label="Menu">{open ? <X className="size-4" /> : <Menu className="size-4" />}</button>
            <div className="min-w-0">
              <div className="min-h-[18px] text-xs text-fg-3">{crumb}</div>
              <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
            </div>
          </div>
          <label>
            <span className="sr-only">Cluster</span>
            <select value={cluster} onChange={(e) => setCluster(e.target.value)} className="h-9 min-w-[180px] cursor-pointer rounded-lg border border-line-2 bg-card px-2.5 focus:border-accent focus:outline-none">
              <option value="">All clusters</option>
              {(state?.clusters ?? []).map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
            </select>
          </label>
        </header>
        <main className="anim-rise flex flex-col gap-4 px-4 pb-12 pt-5 md:px-7" key={loc.pathname}><ErrorBoundary><Outlet /></ErrorBoundary></main>
      </div>
    </div>
  );
}

export function Toasts() {
  const { toasts } = useLive();
  return (
    <div className="pointer-events-none fixed bottom-5 left-1/2 z-[60] flex -translate-x-1/2 flex-col items-center gap-2" role="status" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={clsx('anim-pop pointer-events-auto max-w-[min(640px,92vw)] rounded-lg border bg-raise px-4 py-2.5 shadow-pop', t.error ? 'border-bad' : 'border-line-2')}>{t.text}</div>
      ))}
    </div>
  );
}
