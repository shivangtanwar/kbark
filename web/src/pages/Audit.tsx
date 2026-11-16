import { useNavigate } from 'react-router-dom';
import { Card, CardHeader, Empty, Mono, Pill, Sub, Table, Td, Th } from '../components/ui';
import { dateTime } from '../lib/format';
import { useLive, useScoped } from '../lib/live';
import { EVENTS } from '../lib/vocab';

export function Audit() {
  const { audit } = useLive();
  const nav = useNavigate();
  const rows = useScoped(audit.entries);
  return (
    <Card>
      <CardHeader title="Every detection, proposal, approval and outcome" hint={<Mono className="text-xs">{audit.path || 'In memory only'}</Mono>} />
      {rows.length ? (
        <Table head={<><Th>Time</Th><Th>Incident</Th><Th>Event</Th><Th>By</Th><Th>Workload</Th><Th>Detail</Th></>}>
          {rows.map((e, n) => {
            const ev = EVENTS[e.event] ?? { label: e.event, tone: 'idle' as const };
            return (
              <tr key={n} className="group cursor-pointer hover:bg-raise" onClick={() => nav(`/incidents/${e.incident}`)}>
                <Td className="whitespace-nowrap"><Mono className="text-fg-2">{dateTime(e.time)}</Mono></Td>
                <Td className="whitespace-nowrap"><Mono className="text-fg-2">{e.incident}</Mono></Td>
                <Td><Pill tone={ev.tone}>{ev.label}</Pill></Td>
                <Td className="whitespace-nowrap">{e.actor === 'kbark' ? <span className="text-fg-3">kbark</span> : e.actor}</Td>
                <Td><Mono>{e.workload.toLowerCase()}</Mono><Sub>{e.cluster} · {e.namespace}</Sub></Td>
                <Td className="max-w-[340px] truncate text-fg-2"><span title={e.note}>{e.note}</span></Td>
              </tr>
            );
          })}
        </Table>
      ) : <Empty title="Nothing recorded yet">The first detection will appear here.</Empty>}
    </Card>
  );
}
