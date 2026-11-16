import { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { HealthBar } from '../components/charts';
import { Card, CardBody, CardHeader, Chip, Empty, Mono, Pill, Skeleton, Table, Td, Th } from '../components/ui';
import { useLive } from '../lib/live';

export function Clusters() {
  const { state, cluster, setCluster } = useLive();
  const nav = useNavigate();
  const byCluster = useMemo(() => {
    const m = new Map<string, Map<string, { total: number; failing: number }>>();
    for (const p of state?.pods ?? []) {
      const ns = m.get(p.cluster) ?? new Map();
      const n = ns.get(p.namespace) ?? { total: 0, failing: 0 };
      n.total++; if (p.health === 'failing') n.failing++;
      ns.set(p.namespace, n); m.set(p.cluster, ns);
    }
    return m;
  }, [state]);
  if (!state) return <Skeleton className="h-64" />;
  const clusters = state.clusters.filter((c) => !cluster || c.name === cluster);

  return (
    <div className="grid gap-4 [grid-template-columns:repeat(auto-fit,minmax(360px,1fr))]">
      {clusters.map((c) => {
        const open = state.incidents.filter((i) => i.cluster === c.name && !i.closedAt).length;
        const namespaces = [...(byCluster.get(c.name) ?? new Map()).entries()].sort((a, b) => a[0].localeCompare(b[0]));
        return (
          <Card key={c.name}>
            <CardHeader title={<Mono className="text-[13px] font-semibold">{c.name}</Mono>}>
              {c.state === 'connected' ? <Pill tone="ok">Connected</Pill> : <Pill tone={c.error ? 'bad' : 'idle'}>{c.error ? 'Cannot connect' : 'Connecting'}</Pill>}
            </CardHeader>
            <CardBody className="flex flex-col gap-3">
              {c.error && <p className="rounded-lg border border-bad/30 bg-bad-soft px-3 py-2 text-[13px]">{c.error}</p>}
              <div>
                <div className="mb-1.5 flex items-baseline justify-between"><span className="font-medium">Pods</span><span className="num text-xs text-fg-2">{c.healthy} / {c.pods} healthy</span></div>
                <HealthBar c={c} />
              </div>
              <div className="flex flex-wrap gap-1.5">
                <Chip label="Nodes ready">{c.nodesReady} / {c.nodes}</Chip>
                {c.cordoned > 0 && <Chip label="Cordoned">{c.cordoned}</Chip>}
                <Chip label="Open incidents">{open}</Chip>
              </div>
            </CardBody>
            {namespaces.length ? (
              <div className="border-t border-line">
                <Table head={<><Th>Namespace</Th><Th right>Pods</Th><Th right>Failing</Th></>}>
                  {namespaces.map(([ns, n]) => (
                    <tr key={ns} className="group cursor-pointer hover:bg-raise" onClick={() => { setCluster(c.name); nav(`/pods?q=${encodeURIComponent(ns)}`); }}>
                      <Td><Mono>{ns}</Mono></Td>
                      <Td right className="num">{n.total}</Td>
                      <Td right className="num">{n.failing ? <Pill tone="bad">{n.failing}</Pill> : <span className="text-fg-3">0</span>}</Td>
                    </tr>
                  ))}
                </Table>
              </div>
            ) : <Empty title="No pods">Nothing is running in this cluster yet.</Empty>}
          </Card>
        );
      })}
    </div>
  );
}
