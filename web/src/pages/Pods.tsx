import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Card, Empty, Input, LinkButton, Mono, Pill, Seg, Sub, Table, Td, Th } from '../components/ui';
import { ago } from '../lib/format';
import { useLive, useScoped } from '../lib/live';
import type { Tone } from '../lib/vocab';

const HEALTH: Record<string, [Tone, string]> = { healthy: ['ok', 'Healthy'], failing: ['bad', 'Failing'], pending: ['idle', 'Starting'], done: ['idle', 'Completed'] };
const unwell = (h: string) => h !== 'healthy' && h !== 'done';

export function Pods() {
  const { state, now } = useLive();
  const [params, setParams] = useSearchParams();
  const q = params.get('q') ?? '';
  const [only, setOnly] = useState<'all' | 'bad'>('all');
  const scoped = useScoped(state?.pods);
  const incidentOf = useMemo(() => {
    const m = new Map<string, string>();
    for (const i of state?.incidents ?? []) if (!i.closedAt) for (const p of i.pods) m.set(`${i.cluster}/${i.namespace}/${p}`, i.id);
    return m;
  }, [state]);
  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return scoped.filter((p) => (only === 'all' || unwell(p.health)) && (!needle || `${p.name} ${p.namespace}`.toLowerCase().includes(needle)));
  }, [scoped, only, q]);

  return (
    <>
      <div className="flex flex-wrap items-center gap-2.5">
        <Seg label="Filter pods" value={only} onChange={setOnly} options={[{ value: 'all', label: 'All', count: scoped.length }, { value: 'bad', label: 'Not healthy', count: scoped.filter((p) => unwell(p.health)).length }]} />
        <Input type="search" value={q} onChange={(e) => setParams(e.target.value ? { q: e.target.value } : {}, { replace: true })} placeholder="Search by pod or namespace" aria-label="Search pods" className="ml-auto max-w-[280px]" />
      </div>
      <Card>
        {rows.length ? (
          <Table head={<><Th>Pod</Th><Th>Health</Th><Th>Status</Th><Th right>Ready</Th><Th right>Restarts</Th><Th right>Age</Th><Th>Controlled by</Th><Th>Incident</Th></>}>
            {rows.map((p) => {
              const [tone, label] = HEALTH[p.health] ?? HEALTH.pending;
              const inc = incidentOf.get(`${p.cluster}/${p.namespace}/${p.name}`);
              return (
                <tr key={`${p.cluster}/${p.namespace}/${p.name}`} className="group">
                  <Td><Mono>{p.name}</Mono><Sub>{p.cluster} · {p.namespace}</Sub></Td>
                  <Td><Pill tone={tone}>{label}</Pill></Td>
                  <Td className="whitespace-nowrap"><Mono>{p.status}</Mono></Td>
                  <Td right className="num">{p.ready}</Td>
                  <Td right className="num">{p.restarts}</Td>
                  <Td right className="num text-fg-2">{ago(p.created, now)}</Td>
                  <Td><Mono className="text-fg-2">{p.owner.kind === 'Pod' ? 'None' : `${p.owner.kind.toLowerCase()}/${p.owner.name}`}</Mono></Td>
                  <Td>{inc && <LinkButton to={`/incidents/${inc}`}>{inc}</LinkButton>}</Td>
                </tr>
              );
            })}
          </Table>
        ) : <Empty title="No pods match">Try a different filter or search.</Empty>}
      </Card>
    </>
  );
}
