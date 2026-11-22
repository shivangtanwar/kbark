import { TriangleAlert } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { Link, useParams } from 'react-router-dom';
import { DiffView } from '../components/DiffView';
import { Stepper } from '../components/Stepper';
import { Button, Card, CardBody, CardHeader, Chip, Dialog, Empty, Input, Mono, Pill, Skeleton, StatePill } from '../components/ui';
import type { Incident } from '../lib/api';
import { ago, capitalize, clock, duration } from '../lib/format';
import { useLive } from '../lib/live';
import { actionName, family, STATES, workloadName } from '../lib/vocab';

function Note({ tone, children }: { tone: 'warn' | 'person'; children: ReactNode }) {
  const cls = tone === 'warn' ? 'border-warn/30 bg-warn-soft [&_svg]:text-warn' : 'border-person/30 bg-person-soft [&_svg]:text-person';
  return <div className={`flex gap-2.5 rounded-lg border px-3 py-2.5 text-[13px] ${cls}`}><TriangleAlert className="mt-0.5 size-4 shrink-0" /><span>{children}</span></div>;
}

function Rows({ rows, mono }: { rows: [string, ReactNode][]; mono?: boolean }) {
  return (
    <dl className="grid grid-cols-[minmax(110px,max-content)_minmax(0,1fr)]">
      {rows.map(([k, v], n) => {
        const last = n === rows.length - 1 ? '' : 'border-b border-line';
        return [
          <dt key={`k${n}`} className={`px-4 py-2 text-fg-2 ${last}`}>{k}</dt>,
          <dd key={`v${n}`} className={`break-words px-4 py-2 ${mono ? 'font-mono text-[12.5px]' : ''} ${last}`}>{v}</dd>,
        ];
      })}
    </dl>
  );
}

// Timeline entries repeat text already on the page; keep them short.
function brief(note?: string): string {
  if (!note) return '';
  if (note.startsWith('approved: ')) return 'Approved the change';
  if (note.startsWith('applied: ')) return 'Applied the change';
  const cut = note.search(/[.:;] /);
  return capitalize(cut > 20 && note.length > 110 ? note.slice(0, cut) : note);
}

function Decision({ i }: { i: Incident }) {
  const { act, signer, canApprove, me } = useLive();
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<'approve' | 'rollback' | null>(null);
  const [reason, setReason] = useState('');
  const [standing, setStanding] = useState(false);
  const a = i.action;

  const run = async (verb: 'approve' | 'reject' | 'rollback' | 'replan' | 'pull-request') => {
    setBusy(true); setConfirm(null);
    await act(i.id, verb, verb === 'reject' ? reason : undefined, verb === 'approve' && standing);
    setBusy(false);
  };
  const who = (
    <div className="flex items-center justify-between gap-3 text-[12.5px] text-fg-2">
      <span>Signing as</span>
      {signer ? <b className="truncate font-medium text-fg">{signer}</b> : <b className="font-medium text-bad">Name required</b>}
    </div>
  );
  const blocked = !canApprove ? 'Your role is viewer. Approving needs the approver role.' : !signer ? (me?.authEnabled ? '' : 'Enter your name under "Signing as" in the sidebar.') : '';
  const fine = (t: string) => <p className="text-xs text-fg-3">{t}</p>;

  if (i.state === 'proposed' && a) {
    return (
      <Card>
        <CardHeader title="Decision" hint={a.risk === 'low' ? 'Low risk' : 'Medium risk'} />
        <CardBody className="flex flex-col gap-3">
          {who}
          {a.source && <>
            <Button variant="primary" disabled={busy || !!blocked} onClick={() => run('pull-request')}>{busy ? 'Working…' : 'Open pull request'}</Button>
            {fine(`This workload is kept in Git (${a.source.repo}, ${a.source.path}). A pull request there changes nothing in the cluster and avoids drift.`)}
          </>}
          <Button variant={a.source ? 'default' : 'primary'} disabled={busy || !!blocked} onClick={() => setConfirm('approve')}>{busy && !a.source ? 'Applying…' : a.source ? 'Apply live instead' : 'Approve and apply'}</Button>
          <Input value={reason} onChange={(e) => setReason(e.target.value)} maxLength={200} placeholder="Reason for rejecting (optional)" aria-label="Reason for rejecting" disabled={!!blocked} />
          <Button disabled={busy || !!blocked} onClick={() => run('reject')}>Reject</Button>
          {fine(blocked || (a.reversible ? 'Applies exactly the change shown. Can be rolled back.' : 'Applies exactly the change shown. Cannot be undone.'))}
        </CardBody>
        <Dialog open={confirm === 'approve'} onClose={() => setConfirm(null)} title="Apply this change?"
          footer={<><Button onClick={() => setConfirm(null)}>Cancel</Button><Button variant="primary" data-autofocus onClick={() => run('approve')}>Approve and apply</Button></>}>
          <p className="font-medium">{a.title}</p>
          <div className="mt-3 rounded-lg border border-line">
            <Rows rows={[['Cluster', i.cluster], ['Target', `${a.targetKind.toLowerCase()}/${a.targetName}`], ['Reversible', a.reversible ? 'Yes, exactly' : 'No'], ['Recorded as', signer]]} />
          </div>
          {i.standingAllowed && (
            <label className="mt-3 flex cursor-pointer items-start gap-2.5 rounded-lg border border-line p-3">
              <input type="checkbox" className="mt-0.5 size-4 accent-[var(--accent)]" checked={standing} onChange={(e) => setStanding(e.target.checked)} />
              <span className="text-[13px] leading-relaxed">
                <b className="font-medium">Also apply this automatically next time</b>
                <span className="block text-fg-2">If kbark proposes “{actionName(a.type)}” for {workloadName(i)} again, it is applied without asking, recorded under your name. It stops after 30 days, when anyone revokes it in Settings, or the first time such a fix fails.</span>
              </span>
            </label>
          )}
        </Dialog>
      </Card>
    );
  }
  if (i.state === 'pull-request' && i.pullRequest) {
    const merged = i.pullRequest.state === 'merged';
    return (
      <Card>
        <CardHeader title="Decision" />
        <CardBody className="flex flex-col gap-3">
          <div><Pill tone={merged ? 'ok' : 'warn'}>{merged ? 'Merged, waiting for sync' : 'Pull request open'}</Pill></div>
          {/* The address comes from the Git host's answer; only ever link to https. */}
          {i.pullRequest.url.startsWith('https://')
            ? <a href={i.pullRequest.url} target="_blank" rel="noreferrer noopener" className="break-all text-accent hover:underline">{i.pullRequest.repo} #{i.pullRequest.number}</a>
            : <Mono>{i.pullRequest.repo} #{i.pullRequest.number}</Mono>}
          {fine(merged ? 'Merged at the source. This incident closes when the change reaches the cluster and the failure clears.' : 'Nothing was changed in the cluster. This incident closes when the pull request is merged, synced, and the failure clears. If it is closed without merging, the fix is proposed here again.')}
        </CardBody>
      </Card>
    );
  }
  if (i.state === 'applying') {
    return <Card><CardHeader title="Decision" /><CardBody className="flex flex-col gap-3"><div><Pill tone="warn">Applied, verifying</Pill></div>{fine('Waiting for the rollout to finish and the failure to stay clear.')}{i.standingID && fine(`Applied without a click, under standing approval ${i.standingID}.`)}</CardBody></Card>;
  }
  if (i.state === 'verified' || i.state === 'failed') {
    return (
      <Card>
        <CardHeader title="Outcome" />
        <CardBody className="flex flex-col gap-3">
          {i.state === 'verified' && i.closedAt
            ? <div><div className="text-xs text-fg-3">Recovered in</div><div className="num text-[22px] font-semibold tracking-tight">{duration(new Date(i.closedAt).getTime() - new Date(i.openedAt).getTime())}</div></div>
            : <div><Pill tone="bad">Failure did not clear</Pill></div>}
          {i.standingID && fine(`Applied without a click, under standing approval ${i.standingID}. The timeline names who granted it.`)}
          {i.canRollback && <>
            {who}
            <Button disabled={busy || !!blocked} onClick={() => setConfirm('rollback')}>Roll back this change</Button>
            {fine(blocked || 'Restores the exact state from before the change.')}
          </>}
        </CardBody>
        <Dialog open={confirm === 'rollback'} onClose={() => setConfirm(null)} title="Roll back this change?"
          footer={<><Button onClick={() => setConfirm(null)}>Cancel</Button><Button variant="primary" data-autofocus onClick={() => run('rollback')}>Roll back</Button></>}>
          <p>This restores <Mono>{a ? `${a.targetKind.toLowerCase()}/${a.targetName}` : workloadName(i)}</Mono> in <b className="font-medium">{i.cluster}</b> to its state before “{a?.title}”. If that state was failing, the failure returns.</p>
        </Dialog>
      </Card>
    );
  }
  if (i.state === 'manual' || i.state === 'rejected') {
    return (
      <Card>
        <CardHeader title="Decision" />
        <CardBody className="flex flex-col gap-3">
          {fine(i.state === 'manual' ? 'This needs a person. If the situation has changed, kbark can plan again.' : 'Rejected. Nothing was changed.')}
          {who}
          <Button disabled={busy || !!blocked} onClick={() => run('replan')}>Plan again</Button>
          {blocked && fine(blocked)}
        </CardBody>
      </Card>
    );
  }
  return <Card><CardHeader title="Outcome" /><CardBody><StatePill state={i.state} /></CardBody></Card>;
}

export function useIncident(): Incident | undefined | null {
  const { id } = useParams();
  const { state } = useLive();
  if (!state) return undefined;
  return state.incidents.find((x) => x.id === id) ?? null;
}

export function IncidentDetail() {
  const i = useIncident();
  const { now } = useLive();
  if (i === undefined) return <Skeleton className="h-72" />;
  if (i === null) return <Card><Empty title="This incident is not available">It may have been closed long ago. <Link to="/incidents" className="text-accent">Back to incidents</Link></Empty></Card>;
  const a = i.action;

  return (
    <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
      <div className="flex min-w-0 flex-col gap-4">
        <Card>
          <CardBody className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-4">
            <div>
              <h2 className="max-w-[80ch] text-[17px] font-semibold leading-snug">{capitalize(i.summary)}</h2>
              <div className="mt-2.5 flex flex-wrap gap-1.5">
                <Chip label="Failure">{family(i.family)}</Chip>
                <Chip label="Cluster">{i.cluster}</Chip>
                <Chip label="Namespace">{i.namespace}</Chip>
                {i.container && <Chip label={i.init ? 'Init container' : 'Container'}>{i.container}</Chip>}
              </div>
            </div>
            <StatePill state={i.state} />
          </CardBody>
          <Stepper incident={i} />
        </Card>

        {a ? (
          <Card>
            <CardHeader title={i.state === 'proposed' ? 'Proposed change' : 'Change'} hint="Validated by the API server as a dry run" />
            <CardBody className="flex flex-col gap-3">
              <div>
                <p className="text-[14.5px] font-semibold">{a.title}</p>
                <p className="mt-1 max-w-[78ch] text-fg-2">{a.rationale}</p>
              </div>
              {(a.warnings ?? []).map((w, n) => <Note key={n} tone="warn">{w}</Note>)}
              <DiffView action={a} />
            </CardBody>
          </Card>
        ) : i.manualReason ? (
          <Card><CardHeader title="No automatic fix" hint="kbark does not guess" /><CardBody><Note tone="person">{i.manualReason}</Note></CardBody></Card>
        ) : null}

        <Card>
          <CardHeader title="Evidence" hint="From the Kubernetes API · no AI involved" />
          <Rows mono rows={i.evidence.map((e) => [e.label, e.value])} />
        </Card>

        <Card>
          <CardHeader title="Explanation" hint={i.explanationState === 'ready' ? `${i.explanationNote} · advice only` : 'AI · advice only'} />
          <CardBody>
            {i.explanationState === 'ready'
              ? <p className="max-w-[78ch] whitespace-pre-wrap text-fg-2">{i.explanation}</p>
              : <p className="text-fg-2">{i.explanationState === 'pending' ? 'Being written. The evidence and the change do not depend on it.' : i.explanationNote || 'Not available.'}</p>}
          </CardBody>
        </Card>
      </div>

      <div className="flex min-w-0 flex-col gap-4 xl:sticky xl:top-[88px]">
        <Decision i={i} />
        <Card>
          <CardHeader title="Details" />
          <Rows rows={[
            ['Incident', <Mono>{i.id}</Mono>],
            ['Workload', <Mono>{workloadName(i)}</Mono>],
            ...(a ? [['Action', actionName(a.type)], ['Reversible', a.reversible ? 'Yes' : 'No']] as [string, ReactNode][] : []),
            [i.workload.kind === 'Service' ? 'Related pods' : 'Pods', <Mono>{(i.pods ?? []).join(', ') || '–'}</Mono>],
            ['Opened', `${clock(i.openedAt)} (${ago(i.openedAt, now)} ago)`],
          ]} />
        </Card>
        <Card>
          <CardHeader title="Timeline" />
          <ol className="flex flex-col gap-3.5 px-4 py-3.5">
            {i.history.map((h, n) => (
              <li key={n} className="relative grid grid-cols-[14px_minmax(0,1fr)] gap-2.5">
                {n < i.history.length - 1 && <span className="absolute bottom-[-12px] left-[6.5px] top-[18px] w-px bg-line" />}
                <span className={`ml-[3px] mt-1.5 size-2 rounded-full ${h.actor === 'kbark' ? 'bg-line-2' : 'bg-accent'}`} />
                <div>
                  <div className="text-xs text-fg-3">{clock(h.time)} · <b className="font-medium text-fg-2">{h.actor}</b></div>
                  <div className="break-words">{brief(h.note) || STATES[h.state]?.label || h.state}</div>
                </div>
              </li>
            ))}
          </ol>
        </Card>
      </div>
    </div>
  );
}
