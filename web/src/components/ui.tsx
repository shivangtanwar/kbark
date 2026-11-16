import clsx from 'clsx';
import { useEffect, useRef, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Link } from 'react-router-dom';
import { STATES, type Tone } from '../lib/vocab';
import type { IncidentState } from '../lib/api';

const TONE: Record<Tone, string> = {
  ok: 'bg-ok-soft text-ok', warn: 'bg-warn-soft text-warn', bad: 'bg-bad-soft text-bad',
  person: 'bg-person-soft text-person', idle: 'bg-idle-soft text-idle',
};
export const TONE_BG: Record<Tone, string> = { ok: 'bg-ok', warn: 'bg-warn', bad: 'bg-bad', person: 'bg-person', idle: 'bg-idle' };

// Each tone has its own shape so state never rests on colour alone.
const SHAPE: Record<Tone, string> = {
  ok: 'M6 1a5 5 0 100 10A5 5 0 006 1zm2.6 3.9L5.5 8.2 3.6 6.3l.8-.8 1.1 1.1 2.3-2.5z',
  warn: 'M6 .8l5.2 5.2L6 11.2.8 6z',
  bad: 'M6 1l5.4 9.6H.6z',
  person: 'M1.5 1.5h9v9h-9z',
  idle: 'M6 1.5a4.5 4.5 0 100 9 4.5 4.5 0 000-9z',
};

export function Pill({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span className={clsx('inline-flex items-center gap-1.5 whitespace-nowrap rounded-full py-0.5 pl-2 pr-2.5 text-xs font-medium', TONE[tone])}>
      <svg viewBox="0 0 12 12" className="size-[11px] shrink-0 fill-current" aria-hidden="true"><path d={SHAPE[tone]} /></svg>
      {children}
    </span>
  );
}
export const StatePill = ({ state }: { state: IncidentState }) => <Pill tone={STATES[state]?.tone ?? 'idle'}>{STATES[state]?.label ?? state}</Pill>;

export function Card({ children, className }: { children: ReactNode; className?: string }) {
  return <section className={clsx('min-w-0 rounded-xl border border-line bg-card', className)}>{children}</section>;
}
export function CardHeader({ title, hint, children }: { title: ReactNode; hint?: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex min-h-[46px] items-center justify-between gap-3 border-b border-line px-4 py-2.5">
      <h2 className="text-[13px] font-semibold">{title}</h2>
      <div className="flex items-center gap-3 text-xs text-fg-3">{hint}{children}</div>
    </div>
  );
}
export const CardBody = ({ children, className }: { children: ReactNode; className?: string }) => <div className={clsx('p-4', className)}>{children}</div>;

type Variant = 'primary' | 'default' | 'ghost' | 'danger';
const BTN: Record<Variant, string> = {
  primary: 'bg-accent text-accent-fg border-accent hover:brightness-110',
  default: 'bg-raise border-line-2 hover:border-fg-3',
  ghost: 'border-transparent text-fg-2 hover:bg-raise hover:text-fg',
  danger: 'bg-raise border-line-2 text-bad hover:border-bad',
};
export function Button({ variant = 'default', size = 'md', className, ...p }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: 'sm' | 'md' }) {
  return (
    <button
      {...p}
      className={clsx('inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg border font-medium transition-[filter,border-color,background-color] disabled:cursor-not-allowed disabled:opacity-50',
        size === 'sm' ? 'h-7 px-2.5 text-xs' : 'h-9 px-3.5', BTN[variant], className)}
    />
  );
}
export function LinkButton({ to, children, variant = 'default' }: { to: string; children: ReactNode; variant?: Variant }) {
  return <Link to={to} className={clsx('inline-flex h-7 items-center justify-center whitespace-nowrap rounded-lg border px-2.5 text-xs font-medium transition-[filter,border-color]', BTN[variant])}>{children}</Link>;
}

export function Input({ className, ...p }: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...p} className={clsx('h-9 w-full rounded-lg border border-line-2 bg-card px-3 placeholder:text-fg-3 focus:border-accent focus:outline-none', className)} />;
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="px-4 py-10 text-center">
      <div className="font-semibold">{title}</div>
      {children && <div className="mx-auto mt-1 max-w-md text-fg-2">{children}</div>}
    </div>
  );
}

export function Chip({ label, children }: { label: string; children: ReactNode }) {
  return <span className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-md border border-line-2 px-2 py-0.5 text-xs text-fg-2">{label}<b className="font-medium text-fg">{children}</b></span>;
}

export function Seg<T extends string>({ value, onChange, options, label }: { value: T; onChange: (v: T) => void; options: { value: T; label: string; count?: number }[]; label: string }) {
  return (
    <div role="group" aria-label={label} className="inline-flex gap-0.5 rounded-lg border border-line bg-card p-[3px]">
      {options.map((o) => (
        <button key={o.value} aria-pressed={value === o.value} onClick={() => onChange(o.value)}
          className={clsx('inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 font-medium transition-colors', value === o.value ? 'bg-raise text-fg shadow-[inset_0_0_0_1px_var(--line-2)]' : 'text-fg-2 hover:text-fg')}>
          {o.label}{o.count !== undefined && <span className="num text-[11.5px] text-fg-3">{o.count}</span>}
        </button>
      ))}
    </div>
  );
}

export function Th({ children, right }: { children?: ReactNode; right?: boolean }) {
  return <th className={clsx('whitespace-nowrap border-b border-line px-4 py-2.5 text-[11px] font-semibold uppercase tracking-[0.06em] text-fg-3', right ? 'text-right' : 'text-left')}>{children}</th>;
}
export function Td({ children, className, right }: { children?: ReactNode; className?: string; right?: boolean }) {
  return <td className={clsx('border-b border-line px-4 py-2.5 align-middle group-last:border-b-0', right && 'text-right', className)}>{children}</td>;
}
export function Table({ head, children }: { head: ReactNode; children: ReactNode }) {
  return <div className="overflow-x-auto"><table className="w-full border-collapse"><thead><tr>{head}</tr></thead><tbody>{children}</tbody></table></div>;
}
export const Mono = ({ children, className }: { children: ReactNode; className?: string }) => <span className={clsx('font-mono text-[12.5px]', className)}>{children}</span>;
export const Sub = ({ children }: { children: ReactNode }) => <span className="block text-xs text-fg-3">{children}</span>;

export function Dialog({ open, onClose, title, children, footer }: { open: boolean; onClose: () => void; title: string; children: ReactNode; footer: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>('[data-autofocus]')?.focus();
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('keydown', onKey); prev?.focus(); };
  }, [open, onClose]);
  if (!open) return null;
  // Rendered at the document root: an ancestor with a transform (page
  // transitions, sticky columns) would otherwise trap position:fixed.
  return createPortal(
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/60 p-4" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div ref={ref} role="dialog" aria-modal="true" aria-label={title} className="anim-pop w-full max-w-lg rounded-xl border border-line-2 bg-card shadow-pop">
        <div className="border-b border-line px-5 py-3.5 text-[15px] font-semibold">{title}</div>
        <div className="px-5 py-4">{children}</div>
        <div className="flex justify-end gap-2 border-t border-line px-5 py-3">{footer}</div>
      </div>
    </div>,
    document.body,
  );
}

export const Skeleton = ({ className }: { className?: string }) => <div className={clsx('skeleton', className)} />;
