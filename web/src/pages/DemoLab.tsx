import { Check, Loader2, Play, RotateCcw } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, Card, CardBody, Dialog, Empty, Seg, Skeleton, StatePill } from '../components/ui';
import { api, ApiError, type Demo, type Incident, type Scenario } from '../lib/api';
import { ago } from '../lib/format';
import { useLive } from '../lib/live';

const RISK: Record<string, { label: string; cls: string }> = {
  low: { label: 'Low-risk fix', cls: 'text-ok' },
  medium: { label: 'Medium-risk fix', cls: 'text-warn' },
  '': { label: 'No automatic fix', cls: 'text-person' },
};

/** The newest incident on this scenario's workload, open ones first. */
function incidentFor(s: Scenario, incidents: Incident[], cluster: string, namespace: string): Incident | undefined {
  const mine = incidents.filter((i) => i.cluster === cluster && i.namespace === namespace && i.workload.name === s.workload);
  return mine.find((i) => !i.closedAt) ?? mine[0];
}

function ScenarioCard({ s, incident, busy, running, canRun, onRun }: { s: Scenario; incident?: Incident; busy: boolean; running: boolean; canRun: boolean; onRun: () => void }) {
  const risk = RISK[s.risk] ?? RISK[''];
  const helper = s.group === 'Helpers';
  return (
    <Card className="flex flex-col">
      <CardBody className="flex flex-1 flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <h3 className="text-[14.5px] font-semibold leading-snug">{s.title}</h3>
          {!helper && <span className={`shrink-0 whitespace-nowrap text-xs font-medium ${risk.cls}`}>{risk.label}</span>}
        </div>
        <dl className="grid gap-2 text-[13px] leading-relaxed">
          <div><dt className="text-[11px] font-semibold uppercase tracking-[0.06em] text-fg-3">{helper ? 'Does' : 'Breaks'}</dt><dd className="text-fg-2">{s.breaks}</dd></div>
          <div><dt className="text-[11px] font-semibold uppercase tracking-[0.06em] text-fg-3">{helper ? 'Then' : 'kbark proposes'}</dt><dd>{s.expect}</dd></div>
        </dl>
        {s.note && <p className="text-xs text-fg-3">{s.note}</p>}
        <div className="mt-auto flex items-center justify-between gap-3 pt-1">
          <Button size="sm" variant={helper ? 'default' : 'primary'} disabled={busy || !canRun} onClick={onRun}>
            {running ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
            {running ? 'Running' : helper ? 'Run' : 'Inject failure'}
          </Button>
          {incident && (
            <Link to={`/incidents/${incident.id}`} className="flex min-w-0 items-center gap-2 text-xs text-fg-2 hover:text-fg">
              <span className="font-mono">{incident.id}</span><StatePill state={incident.state} />
            </Link>
          )}
        </div>
      </CardBody>
    </Card>
  );
}

export function DemoLab() {
  const { state, now, toast } = useLive();
  const [demo, setDemo] = useState<Demo | null>(null);
  const [missing, setMissing] = useState(false);
  const [target, setTarget] = useState('');
  const [confirmReset, setConfirmReset] = useState(false);

  const load = useCallback(async () => {
    try { setDemo(await api.demo()); } catch (e) { if (e instanceof ApiError && e.status === 404) setMissing(true); }
  }, []);
  // The live stream ticks on every lab step; the timer covers a dropped stream.
  useEffect(() => { load(); }, [load, state]);
  useEffect(() => { const t = setInterval(load, 4000); return () => clearInterval(t); }, [load]);

  const clusters = demo?.clusters ?? [];
  const cluster = clusters.find((c) => c.cluster === target) ?? clusters[0];
  const groups = useMemo(() => {
    const m = new Map<string, Scenario[]>();
    for (const s of demo?.scenarios ?? []) m.set(s.group, [...(m.get(s.group) ?? []), s]);
    return [...m.entries()];
  }, [demo]);

  if (missing) return <Card><Empty title="The demo lab is not enabled">Start kbark with <code className="font-mono">--demo</code> to inject sample failures from here.</Empty></Card>;
  if (!demo || !cluster) return <Skeleton className="h-72" />;

  const running = cluster.running ?? [];
  const resetting = running.some((r) => r.id === 'reset');
  const run = async (id: string, done: string) => {
    try {
      setDemo(await api.demoRun(cluster.cluster, id));
      toast(done);
    } catch (e) { toast(e instanceof Error ? e.message : 'The step could not be started.', true); }
  };
  const title = (id?: string) => (id === 'reset' ? 'Reset' : demo.scenarios.find((s) => s.id === id)?.title ?? id ?? '');

  return (
    <>
      <Card>
        <CardBody className="flex flex-wrap items-center gap-x-5 gap-y-3">
          <div className="min-w-[240px] flex-1">
            <p className="font-medium">Break something on purpose, then watch kbark find it.</p>
            <p className="mt-0.5 text-[13px] text-fg-2">
              Each step changes only the <span className="font-mono">{demo.namespace}</span> namespace of the chosen cluster. Detection, the proposal and your approval then work exactly as they do for a real failure.
            </p>
          </div>
          {clusters.length > 1 && <Seg label="Cluster to break" value={cluster.cluster} onChange={setTarget} options={clusters.map((c) => ({ value: c.cluster, label: c.cluster }))} />}
          <Button variant="danger" disabled={running.length > 0 || !demo.canRun} onClick={() => setConfirmReset(true)}><RotateCcw className="size-4" />Reset namespace</Button>
        </CardBody>
        <div className="flex min-h-[38px] items-center gap-2.5 border-t border-line px-4 py-2 text-[13px]" role="status" aria-live="polite">
          {running.length ? (
            <><Loader2 className="size-4 shrink-0 animate-spin text-accent" /><span>{running.map((r, n) => <span key={r.id}>{n > 0 && ' · '}<b className="font-medium">{title(r.id)}</b>: {r.step}</span>)}</span></>
          ) : cluster.lastError ? (
            <span className="text-bad"><b className="font-medium">{title(cluster.last)}</b> did not finish: {cluster.lastError}</span>
          ) : cluster.last ? (
            <><Check className="size-4 text-ok" /><span className="text-fg-2"><b className="font-medium text-fg">{title(cluster.last)}</b> done on {cluster.cluster}{cluster.finishedAt ? `, ${ago(cluster.finishedAt, now)}` : ''}. {cluster.last !== 'reset' && <>kbark opens an incident as soon as it detects the failure. <Link to="/incidents" className="text-accent">Open incidents</Link></>}</span></>
          ) : (
            <span className="text-fg-3">{demo.canRun ? `Nothing has been injected on ${cluster.cluster} since kbark started.` : 'Your role can watch the lab; injecting failures needs the admin role.'}</span>
          )}
        </div>
      </Card>

      {groups.map(([group, list]) => (
        <section key={group} className="flex flex-col gap-2.5">
          <h2 className="px-0.5 text-[11px] font-semibold uppercase tracking-[0.08em] text-fg-3">{group}</h2>
          <div className="grid gap-3 md:grid-cols-2 2xl:grid-cols-3">
            {list.map((s) => (
              <ScenarioCard key={s.id} s={s} busy={resetting || running.some((r) => r.id === s.id)} running={running.some((r) => r.id === s.id)} canRun={demo.canRun}
                incident={s.group === 'Helpers' ? undefined : incidentFor(s, state?.incidents ?? [], cluster.cluster, demo.namespace)}
                onRun={() => run(s.id, `${s.title}: started on ${cluster.cluster}.`)} />
            ))}
          </div>
        </section>
      ))}

      <Dialog open={confirmReset} onClose={() => setConfirmReset(false)} title={`Reset ${demo.namespace} on ${cluster.cluster}?`}
        footer={<><Button onClick={() => setConfirmReset(false)}>Cancel</Button><Button variant="danger" onClick={() => { setConfirmReset(false); run('reset', `Resetting ${demo.namespace} on ${cluster.cluster}.`); }}>Reset namespace</Button></>}>
        <p>Every workload, Service, ConfigMap and pod in <span className="font-mono">{demo.namespace}</span> is deleted, cordoned nodes are uncordoned, and the healthy baseline is applied again. Open incidents there clear on their own. Nothing outside that namespace is touched.</p>
      </Dialog>
    </>
  );
}
