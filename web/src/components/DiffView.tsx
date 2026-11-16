import clsx from 'clsx';
import type { Action } from '../lib/api';

/** The proposed change as a unified diff. This is what the approver signs. */
export function DiffView({ action }: { action: Action }) {
  const lines = action.diff.split('\n').filter((l) => l !== '' && !l.startsWith('---') && !l.startsWith('+++'));
  const scope = action.targetKind === 'Node' ? 'spec' : action.targetKind === 'Pod' ? 'pods' : 'spec.template';
  return (
    <div className="overflow-hidden rounded-lg border border-line bg-code" role="region" aria-label="Change preview">
      <div className="flex justify-between gap-3 border-b border-line px-3 py-1.5 text-xs text-fg-3">
        <b className="font-mono font-medium text-fg-2">{action.targetKind.toLowerCase()}/{action.targetName}</b>
        <span>{scope}</span>
      </div>
      <div className="overflow-x-auto py-1.5 font-mono text-[12.5px] leading-[1.65]" tabIndex={0}>
        {lines.map((line, i) => {
          const add = line.startsWith('+'), del = line.startsWith('-'), hunk = line.startsWith('@@');
          return (
            <div key={i} className={clsx('whitespace-pre border-l-[3px] px-3',
              add ? 'border-ok bg-ok-soft' : del ? 'border-bad bg-bad-soft' : 'border-transparent', hunk ? 'text-fg-3' : !add && !del && 'text-fg-2')}>
              {line}
            </div>
          );
        })}
      </div>
    </div>
  );
}
