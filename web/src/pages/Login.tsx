import { useState, type FormEvent } from 'react';
import { Button, Input } from '../components/ui';
import { useLive } from '../lib/live';

export function Login() {
  const { signIn } = useLive();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError('');
    try { await signIn(username, password); } catch (err) { setError(err instanceof Error ? err.message : 'Sign-in failed'); setPassword(''); }
    setBusy(false);
  }

  return (
    <div className="grid min-h-full place-items-center p-4">
      <form onSubmit={submit} className="anim-pop w-full max-w-[360px] rounded-2xl border border-line bg-card p-7">
        <div className="mb-6 flex items-center gap-3">
          <svg viewBox="0 0 28 28" className="size-9" aria-hidden="true">
            <rect width="28" height="28" rx="7" fill="var(--accent)" />
            <path d="M14 5.5l7.4 4.25v8.5L14 22.5l-7.4-4.25v-8.5z" fill="none" stroke="var(--accent-fg)" strokeWidth="1.6" strokeLinejoin="round" opacity=".5" />
            <path d="M10.2 14.2l2.6 2.6 5-5.4" fill="none" stroke="var(--accent-fg)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
          <div>
            <h1 className="text-lg font-semibold tracking-tight">Sign in to kbark</h1>
            <p className="text-xs text-fg-3">Approvals are recorded against your account.</p>
          </div>
        </div>
        <label className="mb-3 flex flex-col gap-1.5">
          <span className="text-xs font-medium text-fg-2">Username</span>
          <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" autoFocus required />
        </label>
        <label className="mb-4 flex flex-col gap-1.5">
          <span className="text-xs font-medium text-fg-2">Password</span>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
        </label>
        {error && <p role="alert" className="mb-4 rounded-lg border border-bad/40 bg-bad-soft px-3 py-2 text-[13px] text-bad">{error}</p>}
        <Button variant="primary" type="submit" disabled={busy} className="w-full">{busy ? 'Signing in…' : 'Sign in'}</Button>
      </form>
    </div>
  );
}
