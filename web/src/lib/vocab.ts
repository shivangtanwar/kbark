import type { Incident, IncidentState } from './api';

export type Tone = 'ok' | 'warn' | 'bad' | 'person' | 'idle';
export type Group = 'awaiting' | 'progress' | 'person' | 'closed';

// What each lifecycle state is called on screen. Names describe what a
// person sees and does, not how the engine is built.
export const STATES: Record<IncidentState, { label: string; tone: Tone; group: Group }> = {
  'detected':    { label: 'Planning fix',       tone: 'idle',   group: 'progress' },
  'proposed':    { label: 'Awaiting approval',  tone: 'warn',   group: 'awaiting' },
  'manual':      { label: 'Needs a person',     tone: 'person', group: 'person' },
  'rejected':    { label: 'Rejected',           tone: 'idle',   group: 'person' },
  'applying':    { label: 'Verifying',          tone: 'warn',   group: 'progress' },
  'verified':    { label: 'Fixed',              tone: 'ok',     group: 'closed' },
  'failed':      { label: 'Fix failed',         tone: 'bad',    group: 'awaiting' },
  'rolled-back': { label: 'Rolled back',        tone: 'idle',   group: 'closed' },
  'resolved':    { label: 'Cleared on its own', tone: 'ok',     group: 'closed' },
};

// The audit log names events, which are not the same as states:
// "approved" is something a person did.
export const EVENTS: Record<string, { label: string; tone: Tone }> = {
  'detected':    { label: 'Detected',           tone: 'idle' },
  'proposed':    { label: 'Fix proposed',       tone: 'warn' },
  'manual':      { label: 'No automatic fix',   tone: 'person' },
  'applying':    { label: 'Approved',           tone: 'warn' },
  'applied':     { label: 'Applied',            tone: 'warn' },
  'verified':    { label: 'Verified',           tone: 'ok' },
  'failed':      { label: 'Not verified',       tone: 'bad' },
  'rejected':    { label: 'Rejected',           tone: 'idle' },
  'rolled-back': { label: 'Rolled back',        tone: 'idle' },
  'resolved':    { label: 'Cleared on its own', tone: 'ok' },
  'demo':        { label: 'Demo step',          tone: 'idle' },
};

export const FAMILIES: Record<string, string> = {
  'oom': 'Out of memory', 'crash': 'Application crash', 'image-pull': 'Image pull failure',
  'config': 'Missing configuration', 'probe': 'Health probe failure', 'scheduling': 'Unschedulable',
  'storage': 'Storage failure', 'evicted': 'Evicted pod', 'network': 'Service without endpoints',
  'disk': 'Local disk limit exceeded', 'admission': 'Pods refused at creation',
  'node': 'Node not healthy', 'terminating': 'Pod stuck terminating', 'job': 'Failed job',
};

export const ACTIONS: Record<string, string> = {
  'set-memory-limit': 'Raise memory limit', 'rollback-workload': 'Roll back workload',
  'tune-probe': 'Relax probe timing', 'delete-pod': 'Delete dead pod',
  'restart-pod': 'Replace unhealthy pod', 'uncordon-node': 'Uncordon node',
  'set-ephemeral-storage-limit': 'Raise local disk limit', 'set-image-pull-policy': 'Allow image pull',
  'fix-service-selector': 'Correct Service selector',
  'force-delete-pod': 'Force-delete stuck pod',
};

export const family = (f: string) => FAMILIES[f] ?? f;
export const actionName = (a: string) => ACTIONS[a] ?? a;
export const groupOf = (i: Incident): Group => STATES[i.state]?.group ?? 'progress';
export const workloadName = (i: { workload: { kind: string; name: string } }) => `${i.workload.kind.toLowerCase()}/${i.workload.name}`;
