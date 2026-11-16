import { useEffect, useState, type ReactNode } from 'react';
import { Card, CardBody, CardHeader, Empty, Mono, Pill, Skeleton, Table, Td, Th } from '../components/ui';
import { api, type Settings as SettingsData } from '../lib/api';
import { useLive } from '../lib/live';
import { actionName } from '../lib/vocab';

function Row({ k, children }: { k: string; children: ReactNode }) {
  return <div className="grid grid-cols-[150px_minmax(0,1fr)] gap-3 border-b border-line px-4 py-2.5 last:border-b-0"><span className="text-fg-2">{k}</span><span className="min-w-0 break-words">{children}</span></div>;
}
const list = (v?: string[]) => (v?.length ? v.join(', ') : 'All');

export function Settings() {
  const { me } = useLive();
  const [s, setS] = useState<SettingsData | null>(null);
  const [error, setError] = useState('');
  useEffect(() => { api.settings().then(setS).catch((e) => setError(e instanceof Error ? e.message : 'Could not load settings')); }, []);
  if (error) return <Card><Empty title="Settings could not be loaded">{error}</Empty></Card>;
  if (!s) return <Skeleton className="h-72" />;
  const rules = s.policy.rules ?? [];

  return (
    <>
      <p className="max-w-3xl text-fg-2">These values come from the flags and files kbark was started with. They are shown here so everyone can see the rules the autofix layer is running under; change them where kbark is deployed.</p>
      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader title="Policy" hint={<Mono className="text-xs">{s.policy.source}</Mono>} />
          <Row k="Memory ceiling"><Mono>{s.policy.defaults.memory_ceiling}</Mono></Row>
          <Row k="Fix attempts">{s.policy.defaults.max_attempts} per workload every {s.policy.defaults.attempt_window}</Row>
          <Row k="Approval">Always required. No policy can turn it off.</Row>
        </Card>
        <Card>
          <CardHeader title="Sign-in" />
          <Row k="Mode">{s.authEnabled ? <Pill tone="ok">Accounts with roles</Pill> : <Pill tone="warn">Open, localhost only</Pill>}</Row>
          <Row k="You">{me?.user ? `${me.user.name} (${me.user.role})` : 'Not signed in; the name typed in the sidebar is recorded'}</Row>
          <Row k="Public address">{s.info.publicURL ? <Mono>{s.info.publicURL}</Mono> : 'Not set'}</Row>
        </Card>
      </div>

      <Card>
        <CardHeader title="Policy rules" hint="First match wins" />
        {rules.length ? (
          <Table head={<><Th>Rule</Th><Th>Clusters</Th><Th>Namespaces</Th><Th>Mode</Th><Th>Allowed fixes</Th><Th>Memory ceiling</Th></>}>
            {rules.map((r, n) => (
              <tr key={n} className="group">
                <Td>{r.name || `Rule ${n + 1}`}</Td>
                <Td><Mono>{list(r.clusters)}</Mono></Td>
                <Td><Mono>{list(r.namespaces)}</Mono></Td>
                <Td>{r.mode === 'detect-only' ? <Pill tone="person">Detect only</Pill> : <Pill tone="ok">Propose</Pill>}</Td>
                <Td className="text-fg-2">{r.mode === 'detect-only' ? 'None' : r.actions?.length ? r.actions.map(actionName).join(', ') : 'All'}</Td>
                <Td><Mono>{r.memory_ceiling || 'Default'}</Mono></Td>
              </tr>
            ))}
          </Table>
        ) : <Empty title="No rules">Every namespace uses the defaults above.</Empty>}
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader title="Notifications" />
          <Row k="Webhook">{s.notify.enabled ? <Pill tone="ok">Sending to {s.notify.host}</Pill> : <Pill tone="idle">Off</Pill>}</Row>
          {s.notify.enabled && <>
            <Row k="Events">{(s.notify.events ?? []).sort().join(', ')}</Row>
            <Row k="Delivered"><span className="num">{s.notify.sent} sent, {s.notify.failed} failed, {s.notify.dropped} dropped</span></Row>
            {s.notify.lastError && <Row k="Last error"><span className="text-bad">{s.notify.lastError}</span></Row>}
          </>}
        </Card>
        <Card>
          <CardHeader title="System" />
          <Row k="Version"><Mono>{s.info.version}</Mono></Row>
          <Row k="Clusters"><Mono>{s.clusters.join(', ')}</Mono></Row>
          <Row k="AI explanations">{s.info.ai ? <>{s.info.ai} <span className="text-fg-3">· advice only</span></> : 'Off. Detection and fixes do not need it.'}</Row>
          <Row k="Incident state"><Mono>{s.info.statePath || 'In memory only'}</Mono></Row>
          <Row k="Audit log"><Mono>{s.info.auditPath || 'In memory only'}</Mono></Row>
        </Card>
      </div>

      {s.users && (
        <Card>
          <CardHeader title="Accounts" hint="Visible to admins" />
          <Table head={<><Th>Name</Th><Th>Username</Th><Th>Role</Th></>}>
            {s.users.map((u) => (
              <tr key={u.username} className="group"><Td>{u.name}</Td><Td><Mono>{u.username}</Mono></Td><Td className="capitalize">{u.role}</Td></tr>
            ))}
          </Table>
          <CardBody className="border-t border-line text-xs text-fg-3">Accounts live in the users file. Add one with <Mono className="text-xs">kbark users hash</Mono>.</CardBody>
        </Card>
      )}
    </>
  );
}
