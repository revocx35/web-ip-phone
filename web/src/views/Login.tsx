import { useState } from 'preact/hooks';
import { post, type Me } from '../api';
import { signedIn } from '../session';
import { Field, Input } from '../components/ui';

export function LoginView() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [ticket, setTicket] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e: Event) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (!ticket) {
        const r = await post<{ user?: Me; mfaRequired?: boolean; ticket?: string }>('/auth/login', { username: username.trim(), password, client: 'web' });
        if (r.mfaRequired && r.ticket) {
          setTicket(r.ticket);
          setPassword('');
        } else if (r.user) {
          signedIn(r.user);
        }
      } else {
        const r = await post<{ user: Me }>('/auth/mfa', { ticket, code: code.trim() });
        signedIn(r.user);
      }
    } catch (err) {
      const e2 = err as Error & { code?: string };
      if (e2.code === 'mfa_expired') {
        setTicket('');
        setCode('');
      }
      setError(e2.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="auth-wrap">
      <form class="card auth-card stack" onSubmit={submit}>
        <div class="brand">
          <img src="/icon.svg" alt="" />
          Web IP Phone
        </div>
        {!ticket ? (
          <>
            <Field label="Username">
              <Input value={username} onValue={setUsername} autocomplete="username" autofocus required />
            </Field>
            <Field label="Password">
              <Input type="password" value={password} onValue={setPassword} autocomplete="current-password" required />
            </Field>
          </>
        ) : (
          <>
            <p class="muted">Enter the 6-digit code from your authenticator app, or one of your recovery codes.</p>
            <Field label="Code">
              <Input value={code} onValue={setCode} autocomplete="one-time-code" inputMode="numeric" autofocus required maxLength={20} />
            </Field>
          </>
        )}
        {error && <div class="error-text">{error}</div>}
        <button class="btn primary block" disabled={busy}>
          {busy ? 'Signing in…' : ticket ? 'Verify' : 'Sign in'}
        </button>
        {ticket && (
          <button type="button" class="btn ghost block" onClick={() => (setTicket(''), setCode(''), setError(''))}>
            Back
          </button>
        )}
      </form>
    </div>
  );
}
