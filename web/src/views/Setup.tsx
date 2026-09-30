import { useState } from 'preact/hooks';
import { post, type Me } from '../api';
import { signedIn } from '../session';
import { Field, Input } from '../components/ui';

export function SetupView() {
  const [token, setToken] = useState('');
  const [username, setUsername] = useState('admin');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [password2, setPassword2] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e: Event) => {
    e.preventDefault();
    setError('');
    if (password !== password2) {
      setError('The passwords do not match');
      return;
    }
    setBusy(true);
    try {
      const r = await post<{ user: Me }>('/setup', { token: token.trim(), username: username.trim(), displayName: displayName.trim(), password });
      signedIn(r.user);
    } catch (err) {
      setError((err as Error).message);
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
        <h1 class="center">Create the admin account</h1>
        <p class="muted small">
          This server has no accounts yet. To prove you run it, enter the <b>setup token</b> printed in the server log:
        </p>
        <pre class="small mono" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>docker compose logs webphone | grep setup_token</pre>
        <Field label="Setup token">
          <Input value={token} onValue={setToken} placeholder="XXXX-XXXX-XXXX-XXXX-XXXX" autocomplete="off" spellcheck={false} required />
        </Field>
        <Field label="Username">
          <Input value={username} onValue={setUsername} autocomplete="username" required minLength={3} maxLength={32} />
        </Field>
        <Field label="Display name (optional)">
          <Input value={displayName} onValue={setDisplayName} maxLength={64} />
        </Field>
        <Field label="Password" hint="At least 10 characters. A long passphrase is best.">
          <Input type="password" value={password} onValue={setPassword} autocomplete="new-password" required minLength={10} />
        </Field>
        <Field label="Repeat password">
          <Input type="password" value={password2} onValue={setPassword2} autocomplete="new-password" required />
        </Field>
        {error && <div class="error-text">{error}</div>}
        <button class="btn primary block" disabled={busy}>
          {busy ? 'Creating…' : 'Create admin account'}
        </button>
      </form>
    </div>
  );
}
