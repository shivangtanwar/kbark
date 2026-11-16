import clsx from 'clsx';
import type { Action } from '../lib/api';

type Part = { object: string; scope: string; lines: string[] };

/** Splits a unified diff into one part per object it changes. */
function parts(diff: string): Part[] {
  const out: Part[] = [];
  for (const line of diff.split('\n')) {
    if (line.startsWith('--- ')) {
      // "--- service/checkout spec.selector (current)"
      const [object, ...rest] = line.slice(4).replace(/ \((current|proposed)\)$/, '').split(' ');
      out.push({ object, scope: rest.join(' '), lines: [] });
    } else if (line !== '' && !line.startsWith('+++ ')) {
      if (out.length === 0) out.push({ object: '', scope: '', lines: [] });
      out[out.length - 1].lines.push(line);
    }
  }
  return out;
}

/** The proposed change as a unified diff. This is what the approver signs. */
export function DiffView({ action }: { action: Action }) {
  return (
    <div className="grid gap-2.5" role="region" aria-label="Change preview">
      {parts(action.diff).map((part, n) => (
        <div key={n} className="overflow-hidden rounded-lg border border-line bg-code">
          <div className="flex justify-between gap-3 border-b border-line px-3 py-1.5 text-xs text-fg-3">
            <b className="font-mono font-medium text-fg-2">{part.object || `${action.targetKind.toLowerCase()}/${action.targetName}`}</b>
            <span>{part.scope}</span>
          </div>
          <div className="overflow-x-auto py-1.5 font-mono text-[12.5px] leading-[1.65]" tabIndex={0}>
            {part.lines.map((line, i) => {
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
      ))}
    </div>
  );
}
