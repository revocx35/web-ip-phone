import { useEffect, useState } from 'preact/hooks';
import { del, get, patch, post, put, type Me, type Phone, type RegState, type Session } from '../api';
import { navigate, route, toast, useStore } from '../store';
import { refreshMe, session } from '../session';
import { audio, type AudioPrefs } from '../phone/audio';
import { AsyncButton, Check, Field, fmtTime, Input, Modal, RegBadge, useConfirm } from '../components/ui';
import { ChangePasswordForm, RecoveryCodes, TwoFactorSetup } from './security';

const TABS: [string, string][] = [
  ['account', 'Account'],
  ['security', 'Security'],
  ['phones', 'My phones'],
  ['audio', 'Audio'],
];

export function SettingsView({ me }: { me: Me }) {
  const path = useStore(route);
  const tab = path.split('/')[2] || 'account';
  return (
    <>
      <h1>Settings</h1>
      <div class="tabs" role="tablist">
        {TABS.map(([id, label]) => (
          <button key={id} role="tab" class={tab === id ? 'on' : ''} onClick={() => navigate('/settings/' + id)}>
            {label}
          </button>
        ))}
      </div>
      {tab === 'account' && <Account me={me} />}
      {tab === 'security' && <Security me={me} />}
      {tab === 'phones' && <MyPhones />}
      {tab === 'audio' && <AudioSettings />}
    </>
  );
}

function Account({ me }: { me: Me }) {
  const [name, setName] = useState(me.displayName);
  return (
    <>
      <div class="card stack">
        <h2>Profile</h2>
        <dl class="kv">
          <dt>Username</dt>
          <dd>{me.username}</dd>
          <dt>Role</dt>
          <dd>{me.isAdmin ? 'Administrator' : 'User'}</dd>
        </dl>
        <Field label="Display name">
          <Input value={name} onValue={setName} maxLength={64} />
        </Field>
        <div class="row end">
          <AsyncButton
            class="primary"
            onClick={async () => {
              const m = await patch<Me>('/me', { displayName: name.trim() });
              session.set((s) => ({ ...s, me: m }));
              toast('Saved', 'success');
            }}
          >
            Save
          </AsyncButton>
        </div>
      </div>
      <div class="card">
        <h2>Password</h2>
        <ChangePasswordForm me={me} onDone={(m) => session.set((s) => ({ ...s, me: m }))} />
      </div>
    </>
  );
}

function Security({ me }: { me: Me }) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [setup, setSetup] = useState(false);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [pwFor, setPwFor] = useState<'disable' | 'codes' | null>(null);
  const [pw, setPw] = useState('');
  const { confirm, dialog } = useConfirm();
  const load = () => get<Session[]>('/me/sessions').then(setSessions);
  useEffect(() => {
    load();
  }, []);

  return (
    <>
      <div class="card stack">
        <div class="card-head">
          <h2>Two-factor authentication</h2>
          {me.totpEnabled ? <span class="badge ok">On</span> : <span class="badge warn">Off</span>}
        </div>
        {me.totpEnabled ? (
          <>
            <p class="muted">
              Signing in needs a code from your authenticator app. {me.recoveryCodesLeft} recovery codes left.
            </p>
            <div class="row">
              <button class="btn" onClick={() => setPwFor('codes')}>
                New recovery codes
              </button>
              {!me.twoFaRequired && (
                <button class="btn danger" onClick={() => setPwFor('disable')}>
                  Turn off
                </button>
              )}
            </div>
          </>
        ) : setup ? (
          <TwoFactorSetup onDone={() => (setSetup(false), refreshMe())} />
        ) : (
          <>
            <p class="muted">Protect your account with a second factor. Strongly recommended: this account can place phone calls.</p>
            <div class="row">
              <button class="btn primary" onClick={() => setSetup(true)}>
                Set up
              </button>
            </div>
          </>
        )}
      </div>

      <div class="card pad-0">
        <div class="card-head" style={{ padding: '18px 18px 0' }}>
          <h2>Signed-in devices</h2>
        </div>
        <ul class="list">
          {sessions.map((s) => (
            <li key={s.id}>
              <div class="grow">
                <div style={{ fontWeight: 600 }}>
                  {s.name || (s.kind === 'app' ? 'Android app' : 'Browser')} {s.current && <span class="badge info">This device</span>}
                </div>
                <div class="small muted">
                  {s.kind === 'app' ? 'App' : 'Web'} · last active {fmtTime(s.lastSeenAt)} from {s.lastIp} · signed in {fmtTime(s.createdAt)}
                </div>
              </div>
              {!s.current && (
                <button
                  class="btn small"
                  onClick={() =>
                    confirm(`Sign out "${s.name || s.kind}"?`, async () => {
                      await del('/me/sessions/' + s.id);
                      load();
                    })
                  }
                >
                  Sign out
                </button>
              )}
            </li>
          ))}
        </ul>
      </div>

      {pwFor && (
        <Modal
          title={pwFor === 'disable' ? 'Turn off two-factor authentication' : 'New recovery codes'}
          onClose={() => (setPwFor(null), setPw(''))}
          footer={
            <>
              <button class="btn" onClick={() => (setPwFor(null), setPw(''))}>
                Cancel
              </button>
              <AsyncButton
                class={pwFor === 'disable' ? 'danger' : 'primary'}
                onClick={async () => {
                  if (pwFor === 'disable') {
                    await post('/me/totp/disable', { password: pw });
                    toast('Two-factor authentication is off', 'success');
                  } else {
                    const r = await post<{ recoveryCodes: string[] }>('/me/totp/recovery', { password: pw });
                    setCodes(r.recoveryCodes);
                  }
                  setPwFor(null);
                  setPw('');
                  refreshMe();
                }}
              >
                Continue
              </AsyncButton>
            </>
          }
        >
          <Field label="Confirm with your password">
            <Input type="password" value={pw} onValue={setPw} autocomplete="current-password" />
          </Field>
        </Modal>
      )}
      {codes && (
        <Modal title="Recovery codes" onClose={() => setCodes(null)} footer={<button class="btn primary" onClick={() => setCodes(null)}>Done</button>}>
          <RecoveryCodes codes={codes} />
        </Modal>
      )}
      {dialog}
    </>
  );
}

interface PhoneRow extends Phone {
  pbxName: string;
  owned: boolean;
  usable: boolean;
  reg: RegState;
}
interface PbxRef {
  id: number;
  name: string;
  mode: 'any' | 'selected';
  canAddPhones: boolean;
  restricted: boolean;
}

export function PhoneForm(props: {
  initial?: Partial<Phone>;
  pbxs?: { id: number; name: string }[];
  onSave: (body: any) => Promise<unknown>;
  onClose: () => void;
  title: string;
}) {
  const i = props.initial ?? {};
  const [pbxId, setPbxId] = useState(i.pbxId ?? props.pbxs?.[0]?.id ?? 0);
  const [label, setLabel] = useState(i.label ?? '');
  const [sipUser, setSipUser] = useState(i.sipUser ?? '');
  const [authUser, setAuthUser] = useState(i.authUser ?? '');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState(i.displayName ?? '');
  const [register, setRegister] = useState(i.register ?? true);
  const [verify, setVerify] = useState(true);
  const [error, setError] = useState('');
  const editing = !!i.id;

  const save = async () => {
    setError('');
    const body: any = { label: label.trim(), sipUser: sipUser.trim(), authUser: authUser.trim(), displayName: displayName.trim(), register, verify };
    if (!editing) body.pbxId = pbxId;
    if (password || !editing) body.password = password;
    try {
      await props.onSave(body);
      props.onClose();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <Modal
      title={props.title}
      onClose={props.onClose}
      footer={
        <>
          <button class="btn" onClick={props.onClose}>
            Cancel
          </button>
          <AsyncButton class="primary" onClick={save}>
            {verify ? 'Check & save' : 'Save'}
          </AsyncButton>
        </>
      }
    >
      <form class="stack" onSubmit={(e) => (e.preventDefault(), save())}>
        {!editing && props.pbxs && (
          <Field label="PBX">
            <select class="input" value={String(pbxId)} onChange={(e) => setPbxId(Number((e.target as HTMLSelectElement).value))}>
              {props.pbxs.map((p) => (
                <option key={p.id} value={String(p.id)}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        <div class="form-grid">
          <Field label="Extension / SIP user">
            <Input value={sipUser} onValue={setSipUser} required maxLength={64} autocomplete="off" placeholder="1001" />
          </Field>
          <Field label="Password (SIP secret)" hint={editing ? 'Leave empty to keep the current one' : undefined}>
            <Input type="password" value={password} onValue={setPassword} autocomplete="new-password" maxLength={128} required={!editing} />
          </Field>
          <Field label="Auth username" hint="Only if it differs from the extension">
            <Input value={authUser} onValue={setAuthUser} maxLength={128} autocomplete="off" />
          </Field>
          <Field label="Caller ID name" hint="Shown to the people you call (the PBX may override it)">
            <Input value={displayName} onValue={setDisplayName} maxLength={64} />
          </Field>
          <Field label="Label" hint="Your name for this phone">
            <Input value={label} onValue={setLabel} maxLength={64} placeholder="Office line" />
          </Field>
        </div>
        <Check
          checked={register}
          onChange={setRegister}
          label="Receive incoming calls"
          hint="Registers the extension with the PBX while you are online. If a desk phone uses the same extension, the PBX must allow several contacts (FreePBX: Max Contacts > 1)."
        />
        <Check checked={verify} onChange={setVerify} label="Check the credentials with the PBX before saving" />
        {error && <div class="error-text">{error}</div>}
        <button class="hidden" />
      </form>
    </Modal>
  );
}

function MyPhones() {
  const [data, setData] = useState<{ phones: PhoneRow[]; pbxs: PbxRef[] } | null>(null);
  const [edit, setEdit] = useState<PhoneRow | 'new' | null>(null);
  const { confirm, dialog } = useConfirm();
  const load = () => get<{ phones: PhoneRow[]; pbxs: PbxRef[] }>('/phones').then(setData);
  useEffect(() => {
    load();
  }, []);
  if (!data) return <div class="muted">Loading…</div>;
  const addable = data.pbxs.filter((p) => p.canAddPhones);

  return (
    <>
      <div class="card pad-0">
        <div class="card-head" style={{ padding: '18px 18px 0' }}>
          <h2>Phones you can use</h2>
          {addable.length > 0 && (
            <button class="btn primary" onClick={() => setEdit('new')}>
              Add phone
            </button>
          )}
        </div>
        {data.phones.length === 0 ? (
          <div class="empty">No phones yet.</div>
        ) : (
          <ul class="list">
            {data.phones.map((p) => (
              <li key={p.id}>
                <div class="grow">
                  <div style={{ fontWeight: 600 }}>
                    {p.label || p.displayName || p.sipUser} <span class="muted small">· {p.sipUser} @ {p.pbxName}</span>
                  </div>
                  <div class="small muted">
                    {p.owned ? 'Your own credentials' : 'Provided by your administrator'}
                    {!p.usable && ' · access removed by your administrator'}
                  </div>
                </div>
                <RegBadge status={p.reg.status} error={p.reg.error} register={p.register} />
                {p.owned && (
                  <>
                    {p.usable && (
                      <button class="btn small" onClick={() => setEdit(p)}>
                        Edit
                      </button>
                    )}
                    <button
                      class="btn small danger"
                      onClick={() =>
                        confirm(`Delete phone ${p.sipUser}?`, async () => {
                          await del('/phones/' + p.id);
                          load();
                        })
                      }
                    >
                      Delete
                    </button>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
      {addable.length === 0 && (
        <p class="small muted" style={{ marginTop: '12px' }}>
          Your administrator assigns your phones. You cannot add your own SIP accounts on any PBX.
        </p>
      )}
      {edit && (
        <PhoneForm
          title={edit === 'new' ? 'Add a phone' : 'Edit phone'}
          initial={edit === 'new' ? undefined : edit}
          pbxs={addable}
          onClose={() => setEdit(null)}
          onSave={async (body) => {
            if (edit === 'new') await post('/phones', body);
            else await put('/phones/' + edit.id, body);
            toast('Saved', 'success');
            load();
          }}
        />
      )}
      {dialog}
    </>
  );
}

function AudioSettings() {
  const [devices, setDevices] = useState<MediaDeviceInfo[]>([]);
  const [prefs, setPrefs] = useState<AudioPrefs>(audio.prefs);
  const [needPermission, setNeedPermission] = useState(false);
  const load = async () => {
    const list = await navigator.mediaDevices.enumerateDevices();
    setDevices(list);
    setNeedPermission(list.some((d) => d.kind === 'audioinput' && !d.label));
  };
  useEffect(() => {
    load();
    navigator.mediaDevices.addEventListener('devicechange', load);
    return () => navigator.mediaDevices.removeEventListener('devicechange', load);
  }, []);
  const update = (p: AudioPrefs) => {
    setPrefs(p);
    audio.setPrefs(p);
  };
  const mics = devices.filter((d) => d.kind === 'audioinput');
  const speakers = devices.filter((d) => d.kind === 'audiooutput');

  return (
    <div class="card stack">
      <h2>Audio devices</h2>
      {needPermission && (
        <div class="banner info">
          <span class="grow">Allow microphone access to see device names.</span>
          <AsyncButton
            class="small"
            onClick={async () => {
              const s = await navigator.mediaDevices.getUserMedia({ audio: true });
              s.getTracks().forEach((t) => t.stop());
              load();
            }}
          >
            Allow
          </AsyncButton>
        </div>
      )}
      <Field label="Microphone">
        <select class="input" value={prefs.micId} onChange={(e) => update({ ...prefs, micId: (e.target as HTMLSelectElement).value })}>
          <option value="">System default</option>
          {mics.map((d) => (
            <option key={d.deviceId} value={d.deviceId}>
              {d.label || 'Microphone'}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Speaker" hint={audio.canChooseSpeaker ? undefined : 'This browser always uses the system default output.'}>
        <select
          class="input"
          value={prefs.speakerId}
          disabled={!audio.canChooseSpeaker}
          onChange={(e) => update({ ...prefs, speakerId: (e.target as HTMLSelectElement).value })}
        >
          <option value="">System default</option>
          {speakers.map((d) => (
            <option key={d.deviceId} value={d.deviceId}>
              {d.label || 'Speaker'}
            </option>
          ))}
        </select>
      </Field>
      <div class="row">
        <AsyncButton
          onClick={async () => {
            await audio.unlock();
            audio.ringtone();
            setTimeout(() => audio.stopTone(), 3500);
          }}
        >
          Play ringtone
        </AsyncButton>
      </div>
      <p class="small muted">
        Echo cancellation and noise suppression of your browser are on. With a laptop, a headset still gives the other side the best sound.
      </p>
    </div>
  );
}
