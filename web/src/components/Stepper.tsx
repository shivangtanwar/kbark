import clsx from 'clsx';
import type { Incident } from '../lib/api';
import { clock } from '../lib/format';
import { STATES, type Tone } from '../lib/vocab';

interface Step { label: string; time?: string; done?: boolean; now?: boolean; tone?: Tone }

// The lifecycle is a real sequence, so it is drawn as one. Branches
// (rejected, no automatic fix, cleared on its own) end the line early.
function stepsFor(i: Incident): Step[] {
  const at = (s: string) => i.history.find((h) => h.state === s)?.time;
  const approved = i.history.find((h) => h.note?.startsWith('approved'));
  const tone = STATES[i.state]?.tone;
  const out: Step[] = [{ label: 'Detected', time: i.openedAt, done: true }];
  if (i.state === 'manual' || (i.state === 'resolved' && !i.action)) {
    out.push({ label: i.state === 'manual' ? 'No automatic fix' : 'Cleared on its own', time: at(i.state), now: true, tone });
    return out;
  }
  out.push({ label: 'Fix proposed', time: at('proposed'), done: !!at('proposed'), now: i.state === 'proposed' });
  if (i.state === 'rejected') return [...out, { label: 'Rejected', time: at('rejected'), now: true, tone: 'idle' }];
  if (i.state === 'resolved') return [...out, { label: 'Cleared on its own', time: at('resolved'), now: true, tone: 'ok' }];
  const pr = i.history.find((h) => h.state === 'pull-request');
  if (pr && !approved) {
    // The fix went to the source repository instead of the cluster.
    out.push({ label: `Pull request by ${pr.actor}`, time: pr.time, done: true, now: i.state === 'pull-request' && i.pullRequest?.state !== 'merged' });
    out.push({ label: 'Merged', done: i.pullRequest?.state === 'merged', now: i.state === 'pull-request' && i.pullRequest?.state === 'merged' });
    out.push({ label: 'Verified', time: i.state === 'verified' ? at('verified') : undefined, now: i.state === 'verified', tone: i.state === 'verified' ? 'ok' : undefined });
    return out;
  }
  out.push({ label: approved ? `Approved by ${approved.actor}` : 'Approved', time: approved?.time, done: !!approved, now: i.state === 'applying' });
  const end = ({ verified: 'Verified', failed: 'Not verified', 'rolled-back': 'Rolled back' } as Record<string, string>)[i.state];
  out.push({ label: end ?? 'Verified', time: end ? at(i.state) : undefined, now: !!end, tone: end ? tone : undefined });
  return out;
}

const DOT: Record<Tone, string> = { ok: 'bg-ok border-ok', bad: 'bg-bad border-bad', person: 'bg-person border-person', warn: 'bg-warn border-warn', idle: 'bg-idle border-idle' };

export function Stepper({ incident }: { incident: Incident }) {
  const steps = stepsFor(incident);
  return (
    <ol className="grid auto-cols-fr grid-flow-col px-4 pb-4 pt-1">
      {steps.map((s, n) => {
        const passed = s.done && !s.now;
        return (
          <li key={n} className={clsx('relative pt-6 text-[12.5px]', s.now ? 'font-medium text-fg' : passed ? 'text-fg-2' : 'text-fg-3')}>
            {n < steps.length - 1 && <span className={clsx('absolute left-0 right-0 top-[6px] h-0.5', passed ? 'bg-accent' : 'bg-line-2')} />}
            <span className={clsx('absolute left-0 top-0 size-3.5 rounded-full border-2',
              s.now && s.tone ? DOT[s.tone] : s.now ? 'border-accent bg-card ring-4 ring-accent-soft' : passed ? 'border-accent bg-accent' : 'border-line-2 bg-card')} />
            <span className="block pr-3">{s.label}</span>
            <span className="block font-mono text-[11.5px] font-normal text-fg-3">{s.time ? clock(s.time) : ' '}</span>
          </li>
        );
      })}
    </ol>
  );
}
