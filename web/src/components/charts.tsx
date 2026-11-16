import { useEffect, useMemo, useState } from 'react';
import { Area, AreaChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { api, type ClusterSummary, type Sample } from '../lib/api';
import { clockShort } from '../lib/format';
import { useLive } from '../lib/live';

/** A cluster's pods as one segmented bar: healthy, failing, starting. */
export function HealthBar({ c }: { c: Pick<ClusterSummary, 'pods' | 'healthy' | 'failing' | 'pending'> }) {
  const seg = (n: number, cls: string, label: string) => n > 0 && (
    <span key={label} className={`${cls} min-w-1 rounded-[3px]`} style={{ flex: n }} title={`${n} ${label}`} />
  );
  return (
    <div className="flex h-2.5 gap-0.5" role="img" aria-label={`${c.healthy} healthy, ${c.failing} failing, ${c.pending} starting`}>
      {c.pods ? [seg(c.healthy, 'bg-ok', 'healthy'), seg(c.failing, 'bg-bad', 'failing'), seg(c.pending, 'bg-idle', 'starting')] : <span className="flex-1 rounded-[3px] bg-line" />}
    </div>
  );
}

export function Legend({ items }: { items: { color: string; label: string }[] }) {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-fg-2">
      {items.map((i) => <span key={i.label} className="inline-flex items-center gap-1.5"><i className="size-2.5 rounded-sm" style={{ background: i.color }} />{i.label}</span>)}
    </div>
  );
}

export interface TrendPoint { t: number; healthy: number; failing: number; pending: number; open: number }

/** Loads trend samples for the window and folds clusters together per timestamp. */
export function useTrend(window: string): TrendPoint[] | null {
  const { cluster, state } = useLive();
  const [samples, setSamples] = useState<Sample[] | null>(null);
  // Refetch when the live state changes, but not more often than the sampling interval matters.
  const bucket = state ? Math.floor(new Date(state.now).getTime() / 15000) : 0;
  useEffect(() => {
    let live = true;
    api.history(window).then((h) => { if (live) setSamples(h.samples); }).catch(() => { if (live) setSamples([]); });
    return () => { live = false; };
  }, [window, bucket]);
  return useMemo(() => {
    if (!samples) return null;
    // Bucket to 30 seconds and keep one sample per cluster per bucket
    // (the latest), so two samples that land in the same bucket are
    // not added together. Then sum across clusters.
    const buckets = new Map<number, Map<string, Sample>>();
    for (const s of samples) {
      if (cluster && s.cluster !== cluster) continue;
      const t = Math.floor(new Date(s.t).getTime() / 30000) * 30000;
      const per = buckets.get(t) ?? new Map<string, Sample>();
      per.set(s.cluster, s);
      buckets.set(t, per);
    }
    const names = new Set(samples.filter((s) => !cluster || s.cluster === cluster).map((s) => s.cluster));
    const by = new Map<number, TrendPoint>();
    const last = new Map<string, Sample>();
    for (const t of [...buckets.keys()].sort((a, b) => a - b)) {
      for (const [name, s] of buckets.get(t)!) last.set(name, s);
      // A cluster with no sample in this bucket keeps its previous
      // value; skip buckets from before every cluster has reported.
      if (last.size < names.size) continue;
      const p = { t, healthy: 0, failing: 0, pending: 0, open: 0 };
      for (const s of last.values()) { p.healthy += s.healthy; p.failing += s.failing; p.pending += s.pending; p.open += s.open; }
      by.set(t, p);
    }
    return [...by.values()].sort((a, b) => a.t - b.t);
  }, [samples, cluster]);
}

const axis = { stroke: 'var(--fg-3)', fontSize: 11, tickLine: false, axisLine: false } as const;

function Tip({ active, payload, label }: { active?: boolean; payload?: { name: string; value: number; color: string }[]; label?: number }) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-lg border border-line-2 bg-card px-3 py-2 text-xs shadow-pop">
      <div className="mb-1 font-mono text-fg-3">{clockShort(label ?? 0)}</div>
      {payload.map((p) => (
        <div key={p.name} className="flex items-center justify-between gap-4">
          <span className="inline-flex items-center gap-1.5 text-fg-2"><i className="size-2 rounded-sm" style={{ background: p.color }} />{p.name}</span>
          <b className="num font-medium text-fg">{p.value}</b>
        </div>
      ))}
    </div>
  );
}

const NeedData = () => <div className="grid h-full place-items-center text-center text-fg-2">Trend lines appear after about a minute of watching.</div>;

/** Pod health over time, stacked: the failing band is what to look at. */
export function HealthTrend({ data }: { data: TrendPoint[] }) {
  if (data.length < 2) return <NeedData />;
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={data} margin={{ top: 6, right: 6, bottom: 0, left: -18 }}>
        <CartesianGrid stroke="var(--line)" vertical={false} />
        <XAxis dataKey="t" type="number" scale="time" domain={['dataMin', 'dataMax']} tickFormatter={clockShort} minTickGap={48} {...axis} />
        <YAxis allowDecimals={false} {...axis} />
        <Tooltip content={<Tip />} cursor={{ stroke: 'var(--line-2)' }} isAnimationActive={false} />
        <Area name="Healthy" dataKey="healthy" stackId="pods" stroke="var(--ok)" strokeWidth={2} fill="var(--ok)" fillOpacity={0.14} isAnimationActive={false} />
        <Area name="Starting" dataKey="pending" stackId="pods" stroke="var(--idle)" strokeWidth={2} fill="var(--idle)" fillOpacity={0.2} isAnimationActive={false} />
        <Area name="Failing" dataKey="failing" stackId="pods" stroke="var(--bad)" strokeWidth={2} fill="var(--bad)" fillOpacity={0.3} isAnimationActive={false} />
      </AreaChart>
    </ResponsiveContainer>
  );
}

/** Open incidents over time: one series, so the card title names it. */
export function OpenTrend({ data }: { data: TrendPoint[] }) {
  if (data.length < 2) return <NeedData />;
  return (
    <ResponsiveContainer width="100%" height="100%">
      <LineChart data={data} margin={{ top: 6, right: 6, bottom: 0, left: -18 }}>
        <CartesianGrid stroke="var(--line)" vertical={false} />
        <XAxis dataKey="t" type="number" scale="time" domain={['dataMin', 'dataMax']} tickFormatter={clockShort} minTickGap={48} {...axis} />
        <YAxis allowDecimals={false} domain={[0, (max: number) => Math.max(4, max + 1)]} {...axis} />
        <Tooltip content={<Tip />} cursor={{ stroke: 'var(--line-2)' }} isAnimationActive={false} />
        <Line name="Open incidents" type="stepAfter" dataKey="open" stroke="var(--accent)" strokeWidth={2} dot={false} activeDot={{ r: 4 }} isAnimationActive={false} />
      </LineChart>
    </ResponsiveContainer>
  );
}

/** Horizontal bars for a small set of counts. */
export function CountBars({ rows }: { rows: { label: string; value: number }[] }) {
  const max = Math.max(1, ...rows.map((r) => r.value));
  return (
    <div className="flex flex-col gap-3.5">
      {rows.map((r) => (
        <div key={r.label} className="grid grid-cols-[minmax(0,150px)_minmax(0,1fr)_24px] items-center gap-3">
          <span className="truncate text-fg-2">{r.label}</span>
          <div title={`${r.label}: ${r.value}`}><div className="h-2.5 min-w-1 rounded-r" style={{ width: `${(r.value / max) * 100}%`, background: 'var(--accent)' }} /></div>
          <span className="num text-right font-medium">{r.value}</span>
        </div>
      ))}
    </div>
  );
}
