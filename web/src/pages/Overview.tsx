import { useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { CountBars, HealthBar, HealthTrend, Legend, OpenTrend, useTrend } from '../components/charts';
import { Card, CardBody, CardHeader, Empty, LinkButton, Mono, Seg, Skeleton, Sub, Table, Td, Th } from '../components/ui';
import { ago, clockShort, duration } from '../lib/format';
import { useLive, useScoped } from '../lib/live';
import { actionName, EVENTS, family, groupOf, workloadName } from '../lib/vocab';

function Kpi({ label, value, unit, sub, tone }: { label: string; value: string | number; unit?: string; sub: string; tone?: 'ok' | 'warn' | 'bad' }) {
  const dot = tone === 'ok' ? 'bg-ok' : tone === 'warn' ? 'bg-warn' : 'bg-bad';
  return (
    <Card className="flex flex-col gap-1.5 px-4 py-3.5">
      <div className="text-xs font-medium text-fg-2">{label}</div>
      <div className="num text-[26px] font-semibold leading-none tracking-tight">{value}{unit && <small className="ml-1 text-sm font-medium tracking-normal text-fg-3">{unit}</small>}</div>
      <div className="flex items-center gap-1.5 text-xs text-fg-3">{tone && <i className={`size-2 shrink-0 rounded-full ${dot}`} />}{sub}</div>
    </Card>
  );
}

export function Overview() {
  const { state, audit, now, cluster } = useLive();
  const nav = useNavigate();
  const [window, setWindow] = useState<'1h' | '6h' | '24h'>('1h');
  const trend = useTrend(window);
  const incidents = useScoped(state?.incidents);
  const activity = useScoped(audit.entries);

  const v = useMemo(() => {
    const clusters = (state?.clusters ?? []).filter((c) => !cluster || c.name === cluster);
    const open = incidents.filter((i) => !i.closedAt);
    const fixed = incidents.filter((i) => i.state === 'verified' && i.closedAt);
    const families = new Map<string, number>();
    for (const i of open) families.set(i.family, (families.get(i.family) ?? 0) + 1);
    return {
      clusters, open, fixed,
      awaiting: open.filter((i) => groupOf(i) === 'awaiting'),
      person: open.filter((i) => groupOf(i) === 'person').length,
      pods: clusters.reduce((n, c) => n + c.pods, 0),
      healthy: clusters.reduce((n, c) => n + c.healthy, 0),
      connected: clusters.filter((c) => c.state === 'connected').length,
      mttr: fixed.length ? fixed.reduce((n, i) => n + (new Date(i.closedAt!).getTime() - new Date(i.openedAt).getTime()), 0) / fixed.length : null,
      families: [...families.entries()].sort((a, b) => b[1] - a[1]).map(([f, n]) => ({ label: family(f), value: n })),
    };
  }, [state, incidents, cluster]);

  if (!state) return <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">{Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="h-[92px]" />)}<Skeleton className="col-span-full h-64" /></div>;

  const unhealthy = v.pods - v.healthy;
  return (
    <>
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
        <Kpi label="Clusters" value={v.connected} unit={`/ ${v.clusters.length}`} sub={v.connected === v.clusters.length ? 'All connected' : 'Some not connected'} tone={v.connected === v.clusters.length ? 'ok' : 'bad'} />
        <Kpi label="Healthy pods" value={v.healthy} unit={`/ ${v.pods}`} sub={unhealthy ? `${unhealthy} not healthy` : 'All healthy'} tone={unhealthy ? 'bad' : 'ok'} />
        <Kpi label="Open incidents" value={v.open.length} sub={v.person ? `${v.person} need a person` : 'None need a person'} tone={v.open.length ? 'warn' : 'ok'} />
        <Kpi label="Awaiting approval" value={v.awaiting.length} sub={v.awaiting.length ? 'Fixes ready to review' : 'Nothing to review'} tone={v.awaiting.length ? 'warn' : 'ok'} />
        <Kpi label="Mean time to recover" value={v.mttr === null ? '–' : duration(v.mttr)} sub={v.fixed.length ? `Across ${v.fixed.length} fixed incident${v.fixed.length > 1 ? 's' : ''}` : 'No fixes verified yet'} />
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <Card>
          <CardHeader title="Pod health over time">
            <Seg label="Time range" value={window} onChange={setWindow} options={[{ value: '1h', label: '1h' }, { value: '6h', label: '6h' }, { value: '24h', label: '24h' }]} />
          </CardHeader>
          <CardBody>
            <div className="h-[220px]">{trend ? <HealthTrend data={trend} /> : <Skeleton className="h-full" />}</div>
            <div className="mt-3"><Legend items={[{ color: 'var(--ok)', label: 'Healthy' }, { color: 'var(--idle)', label: 'Starting' }, { color: 'var(--bad)', label: 'Failing' }]} /></div>
          </CardBody>
        </Card>
        <Card>
          <CardHeader title="Open incidents over time" hint={`Last ${window}`} />
          <CardBody><div className="h-[248px]">{trend ? <OpenTrend data={trend} /> : <Skeleton className="h-full" />}</div></CardBody>
        </Card>
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <Card>
          <CardHeader title="Needs your decision" hint={v.awaiting.length ? `${v.awaiting.length} waiting` : undefined} />
          {v.awaiting.length ? (
            <Table head={<><Th>Incident</Th><Th>Workload</Th><Th>Proposed fix</Th><Th right>Age</Th><Th /></>}>
              {v.awaiting.map((i) => (
                <tr key={i.id} className="group cursor-pointer hover:bg-raise" onClick={() => nav(`/incidents/${i.id}`)}>
                  <Td className="whitespace-nowrap"><Mono className="text-fg-2">{i.id}</Mono><Sub>{family(i.family)}</Sub></Td>
                  <Td><Mono>{workloadName(i)}</Mono><Sub>{i.cluster} · {i.namespace}</Sub></Td>
                  <Td>{i.action ? actionName(i.action.type) : '–'}</Td>
                  <Td right className="num whitespace-nowrap text-fg-2">{ago(i.openedAt, now)}</Td>
                  <Td right><LinkButton to={`/incidents/${i.id}`} variant="primary">Review</LinkButton></Td>
                </tr>
              ))}
            </Table>
          ) : <Empty title="Nothing is waiting for approval">When kbark proposes a fix, it appears here.</Empty>}
        </Card>

        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader title="Clusters" hint={<Link to="/clusters" className="hover:text-fg">View all</Link>} />
            <CardBody className="flex flex-col gap-3.5">
              {v.clusters.map((c) => (
                <div key={c.name}>
                  <div className="mb-1.5 flex items-baseline justify-between gap-3">
                    <Mono className="font-medium">{c.name}</Mono>
                    <span className="num text-xs text-fg-2">{c.state === 'connected' ? `${c.healthy} / ${c.pods} healthy` : 'Not connected'}</span>
                  </div>
                  <HealthBar c={c} />
                </div>
              ))}
            </CardBody>
          </Card>
          <Card>
            <CardHeader title="Open incidents by failure type" hint={`${v.open.length} open`} />
            {v.families.length ? <CardBody><CountBars rows={v.families} /></CardBody> : <Empty title="No open incidents">Every watched pod is healthy or already handled.</Empty>}
          </Card>
        </div>
      </div>

      <Card>
        <CardHeader title="Recent activity" hint={<Link to="/audit" className="hover:text-fg">View audit log</Link>} />
        {activity.length ? (
          <ul>
            {activity.slice(0, 8).map((e, n) => (
              <li key={n} className="grid grid-cols-[56px_minmax(0,1fr)] gap-3 border-b border-line px-4 py-2 last:border-b-0">
                <time className="pt-px font-mono text-xs text-fg-3">{clockShort(e.time)}</time>
                <div className="truncate">
                  {EVENTS[e.event]?.label ?? e.event} · <Link to={`/incidents/${e.incident}`} className="font-mono text-[12.5px] text-fg-2 hover:text-accent">{e.workload.toLowerCase()}</Link>
                  {e.actor !== 'kbark' && <span className="text-fg-3"> by {e.actor}</span>}
                </div>
              </li>
            ))}
          </ul>
        ) : <Empty title="No activity yet">Detections and approvals are listed here.</Empty>}
      </Card>
    </>
  );
}
