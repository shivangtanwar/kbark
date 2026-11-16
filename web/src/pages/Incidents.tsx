import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Card, Empty, Input, Mono, Seg, StatePill, Sub, Table, Td, Th, Skeleton } from '../components/ui';
import type { Incident } from '../lib/api';
import { ago } from '../lib/format';
import { useLive, useScoped } from '../lib/live';
import { family, groupOf, workloadName } from '../lib/vocab';

type Filter = 'open' | 'awaiting' | 'progress' | 'person' | 'closed' | 'all';
const FILTERS: { value: Filter; label: string }[] = [
  { value: 'open', label: 'Open' }, { value: 'awaiting', label: 'Awaiting approval' }, { value: 'progress', label: 'In progress' },
  { value: 'person', label: 'Needs a person' }, { value: 'closed', label: 'Closed' }, { value: 'all', label: 'All' },
];
const match = (i: Incident, f: Filter) => f === 'all' || (f === 'open' ? !i.closedAt : groupOf(i) === f);

export function Incidents() {
  const { state, now } = useLive();
  const nav = useNavigate();
  const all = useScoped(state?.incidents);
  const [filter, setFilter] = useState<Filter>('open');
  const [q, setQ] = useState('');
  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return all.filter((i) => match(i, filter) && (!needle || `${i.id} ${workloadName(i)} ${i.namespace} ${i.cluster} ${family(i.family)}`.toLowerCase().includes(needle)));
  }, [all, filter, q]);

  // Until the first snapshot arrives there is nothing to say about emptiness.
  if (!state) return <Skeleton className="h-64" />;
  return (
    <>
      <div className="flex flex-wrap items-center gap-2.5">
        <Seg label="Filter incidents" value={filter} onChange={setFilter} options={FILTERS.map((f) => ({ ...f, count: all.filter((i) => match(i, f.value)).length }))} />
        <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search incidents" aria-label="Search incidents" className="ml-auto max-w-[260px]" />
      </div>
      <Card>
        {rows.length ? (
          <Table head={<><Th>Incident</Th><Th>Status</Th><Th>Workload</Th><Th>Failure</Th><Th>Proposed fix</Th><Th right>Age</Th></>}>
            {rows.map((i) => (
              <tr key={i.id} className="group cursor-pointer hover:bg-raise" onClick={() => nav(`/incidents/${i.id}`)}>
                <Td className="whitespace-nowrap"><Mono className="text-fg-2">{i.id}</Mono></Td>
                <Td><StatePill state={i.state} /></Td>
                <Td><Mono>{workloadName(i)}</Mono><Sub>{i.cluster} · {i.namespace}</Sub></Td>
                <Td className="whitespace-nowrap">{family(i.family)}</Td>
                <Td className="max-w-[360px] truncate text-fg-2">{i.action ? i.action.title : 'None, needs a person'}</Td>
                <Td right className="num whitespace-nowrap text-fg-2">{ago(i.openedAt, now)}</Td>
              </tr>
            ))}
          </Table>
        ) : (
          <Empty title="No incidents match">{filter === 'open' && !q ? 'kbark is watching every pod. An incident opens here when one fails.' : 'Try a different filter or search.'}</Empty>
        )}
      </Card>
    </>
  );
}
