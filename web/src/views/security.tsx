import { useState } from 'preact/hooks';
import { post, type Me } from '../api';
import { toast } from '../store';
import { AsyncButton, Field, Input } from '../components/ui';

export function ChangePasswordForm(props: { me: Me; onDone: (me: Me) => void }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [next2, setNext2] = useState('');
  const [error, setError] = useState('');
  const min = props.me.passwordMinLength;

  const submit = async () => {
    setError('');
    if (next !== next2) {
      setError('The new passwords do not match');
      return;
    }
    try {
      const me = await post<Me>('/me/password', { current, new: next });
      setCurrent('');
      setNext('');
      setNext2('');
      toast('Password changed. Your other devices were signed out.', 'success');
      props.onDone(me);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <form class="stack" onSubmit={(e) => (e.preventDefault(), submit())}>
      <input type="text" autocomplete="username" value={props.me.username} class="hidden" readOnly />
      <Field label="Current password">
        <Input type="password" value={current} onValue={setCurrent} autocomplete="current-password" required />
      </Field>
      <div class="form-grid">
        <Field label="New password" hint={`At least ${min} characters`}>
          <Input type="password" value={next} onValue={setNext} autocomplete="new-password" required minLength={min} />
        </Field>
        <Field label="Repeat new password">
          <Input type="password" value={next2} onValue={setNext2} autocomplete="new-password" required />
        </Field>
      </div>
      {error && <div class="error-text">{error}</div>}
      <div class="row end">
        <AsyncButton class="primary" type="submit" onClick={submit}>
          Change password
        </AsyncButton>
      </div>
    </form>
  );
}

export function RecoveryCodes({ codes }: { codes: string[] }) {
  return (
    <div class="stack">
      <p>
        <b>Save these recovery codes</b> somewhere safe. Each one signs you in once if you lose your phone. They are shown only now.
      </p>
      <div class="codes">
        {codes.map((c) => (
          <span key={c}>{c}</span>
        ))}
      </div>
      <div class="row">
        <button
          class="btn small"
          onClick={() => navigator.clipboard?.writeText(codes.join('\n')).then(() => toast('Copied', 'success'))}
        >
          Copy
        </button>
      </div>
    </div>
  );
}

/** Enrolment: QR code -> confirm a code -> show recovery codes. */
export function TwoFactorSetup(props: { onDone: () => void }) {
  const [setup, setSetup] = useState<{ secret: string; uri: string; qrPng: string } | null>(null);
  const [code, setCode] = useState('');
  const [codes, setCodes] = useState<string[] | null>(null);
  const [error, setError] = useState('');

  if (codes) {
    return (
      <div class="stack">
        <RecoveryCodes codes={codes} />
        <div class="row end">
          <button class="btn primary" onClick={props.onDone}>
            I saved them
          </button>
        </div>
      </div>
    );
  }
  if (!setup) {
    return (
      <div class="stack">
        <p class="muted">
          Use an authenticator app (Aegis, 2FAS, Google Authenticator, 1Password, …). Signing in will then need a 6-digit code from
          your phone as well as your password.
        </p>
        <div class="row">
          <AsyncButton class="primary" onClick={async () => setSetup(await post('/me/totp/setup'))}>
            Set up two-factor authentication
          </AsyncButton>
        </div>
      </div>
    );
  }
  const enable = async () => {
    setError('');
    try {
      const r = await post<{ recoveryCodes: string[] }>('/me/totp/enable', { code: code.trim() });
      setCodes(r.recoveryCodes);
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <div class="stack">
      <p>1. Scan this QR code with your authenticator app:</p>
      <div class="row" style={{ alignItems: 'flex-start', gap: '20px' }}>
        <img class="qr" src={setup.qrPng} alt="QR code for your authenticator app" />
        <div class="stack grow" style={{ minWidth: '200px' }}>
          <span class="small muted">Or enter this key manually:</span>
          <code style={{ wordBreak: 'break-all' }}>{setup.secret.replace(/(.{4})/g, '$1 ').trim()}</code>
          <a class="small" href={setup.uri}>
            Open in authenticator app on this device
          </a>
        </div>
      </div>
      <form class="stack" onSubmit={(e) => (e.preventDefault(), enable())}>
        <Field label="2. Enter the 6-digit code shown by the app">
          <Input value={code} onValue={setCode} inputMode="numeric" autocomplete="one-time-code" maxLength={6} required />
        </Field>
        {error && <div class="error-text">{error}</div>}
        <div class="row end">
          <AsyncButton class="primary" type="submit" onClick={enable}>
            Turn on
          </AsyncButton>
        </div>
      </form>
    </div>
  );
}
