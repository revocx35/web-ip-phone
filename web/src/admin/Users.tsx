import { useEffect, useState } from 'preact/hooks';
import { del, get, patch, post, put, type Access, type Me, type PBX, type User } from '../api';
import { toast, toastError } from '../store';
import { AsyncButton, Check, Field, fmtTime, Input, Modal, useConfirm } from '../components/ui';

interface AdminPhone {
  id: number;
  pbxId: number;
  ownerId: number;
  label: string;
  sipUser: string;
  displayName: string;
  pbxName: string;
  ownerName: string;
}

function generatePassword() {
  const alphabet = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  const b = new Uint32Array(16);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => alphabet[x % alphabet.length]).join('').replace(/(.{4})(?!$)/g, '$1-');
}

export function UsersAdmin({ me }: { me: Me }) {
  const [users, setUsers] = useState<User[] | null>(null);
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<User | null>(null);
  const load = () => get<User[]>('/admin/users').then(setUsers).catch(toastError);
  useEffect(() => {
    load();
  }, []);
  if (!users) return <div class="muted">Loading…</div>;
  return (
    <>
      <div class="card pad-0">
        <div class="card-head" style={{ padding: '18px 18px 0' }}>
          <h2>Users</h2>
          <button class="btn primary" onClick={() => setAdding(true)}>
            Add user
          </button>
        </div>
        <div class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th>User</th>
                <th>Role</th>
                <th>2FA</th>
                <th>Status</th>
                <th>Online</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id} class="clickable" onClick={() => setOpen(u)}>
                  <td>
                    <b>{u.username}</b> {u.displayName && <span class="muted">· {u.displayName}</span>}
                  </td>
                  <td>{u.isAdmin ? <span class="badge info">Admin</span> : 'User'}</td>
                  <td>{u.totpEnabled ? <span class="badge ok">On</span> : <span class="badge">Off</span>}</td>
                  <td>
                    {u.disabled ? <span class="badge bad">Disabled</span> : u.mustChangePassword ? <span class="badge warn">Must change password</span> : 'Active'}
                  </td>
                  <td>{u.connections ? <span class="badge ok">{u.connections} device(s)</span> : <span class="muted">–</span>}</td>
                  <td class="small muted">{fmtTime(u.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      {adding && <AddUser onClose={() => setAdding(false)} onDone={(u) => (load(), setOpen(u))} />}
      {open && <UserDetail me={me} user={open} onClose={() => setOpen(null)} onChange={load} />}
    </>
  );
}

function AddUser(props: { onClose: () => void; onDone: (u: User) => void }) {
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState(generatePassword());
  const [isAdmin, setIsAdmin] = useState(false);
  const [mustChange, setMustChange] = useState(true);
  const [error, setError] = useState('');
  const save = async () => {
    setError('');
    try {
      const u = await post<User>('/admin/users', { username: username.trim(), displayName: displayName.trim(), password, isAdmin, mustChangePassword: mustChange });
      toast(`User ${u.username} created`, 'success');
      props.onClose();
      props.onDone(u);
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <Modal
      title="Add user"
      onClose={props.onClose}
      footer={
        <>
          <button class="btn" onClick={props.onClose}>
            Cancel
          </button>
          <AsyncButton class="primary" onClick={save}>
            Create user
          </AsyncButton>
        </>
      }
    >
      <form class="stack" onSubmit={(e) => (e.preventDefault(), save())}>
        <div class="form-grid">
          <Field label="Username">
            <Input value={username} onValue={setUsername} required autocomplete="off" />
          </Field>
          <Field label="Display name">
            <Input value={displayName} onValue={setDisplayName} maxLength={64} />
          </Field>
        </div>
        <Field label="Initial password" hint="Give it to the user over a safe channel.">
          <div class="row">
            <Input value={password} onValue={setPassword} class="input grow mono" autocomplete="off" />
            <button type="button" class="btn small" onClick={() => setPassword(generatePassword())}>
              New
            </button>
            <button type="button" class="btn small" onClick={() => navigator.clipboard?.writeText(password).then(() => toast('Copied', 'success'))}>
              Copy
            </button>
          </div>
        </Field>
        <Check checked={mustChange} onChange={setMustChange} label="User must choose a new password at first sign-in" />
        <Check checked={isAdmin} onChange={setIsAdmin} label="Administrator" hint="Can manage users, PBXs and all access rights." />
        {error && <div class="error-text">{error}</div>}
        <button class="hidden" />
      </form>
    </Modal>
  );
}

function UserDetail(props: { me: Me; user: User; onClose: () => void; onChange: () => void }) {
  const [u, setU] = useState(props.user);
  const [pw, setPw] = useState<string | null>(null);
  const { confirm, dialog } = useConfirm();
  const self = u.id === props.me.id;
  const update = async (body: object) => {
    const nu = await patch<User>('/admin/users/' + u.id, body);
    setU(nu);
    props.onChange();
  };
  return (
    <Modal title={u.username} onClose={props.onClose} wide>
      <div class="stack">
        <div class="row">
          <Check checked={u.isAdmin} disabled={self} onChange={(v) => update({ isAdmin: v }).catch(toastError)} label="Administrator" />
          <Check checked={u.disabled} disabled={self} onChange={(v) => update({ disabled: v }).catch(toastError)} label="Disabled (cannot sign in)" />
        </div>
        <div class="row">
          <button class="btn small" onClick={() => setPw(generatePassword())}>
            Reset password
          </button>
          {u.totpEnabled && (
            <button class="btn small" onClick={() => confirm(`Turn off two-factor authentication of ${u.username}?`, async () => {
              await post(`/admin/users/${u.id}/reset-2fa`);
              setU({ ...u, totpEnabled: false });
              props.onChange();
            })}>
              Reset 2FA
            </button>
          )}
          <button class="btn small" onClick={() => confirm(`Sign ${u.username} out on all devices?`, async () => {
            await del(`/admin/users/${u.id}/sessions`);
            toast('Signed out everywhere', 'success');
          })}>
            Sign out everywhere
          </button>
          {!self && (
            <button class="btn small danger" onClick={() => confirm(`Delete ${u.username}? Their own phones and history entries go with the account.`, async () => {
              await del('/admin/users/' + u.id);
              props.onChange();
              props.onClose();
            })}>
              Delete user
            </button>
          )}
        </div>
        <hr style={{ border: 0, borderTop: '1px solid var(--border)', width: '100%' }} />
        <AccessEditor userId={u.id} />
      </div>
      {pw !== null && (
        <Modal
          title={'New password for ' + u.username}
          onClose={() => setPw(null)}
          footer={
            <>
              <button class="btn" onClick={() => setPw(null)}>
                Cancel
              </button>
              <AsyncButton
                class="primary"
                onClick={async () => {
                  await post(`/admin/users/${u.id}/password`, { password: pw, mustChange: true });
                  toast('Password reset. The user is signed out and must choose a new one.', 'success');
                  setPw(null);
                }}
              >
                Set password
              </AsyncButton>
            </>
          }
        >
          <Field label="Temporary password" hint="The user must change it at the next sign-in.">
            <Input value={pw} onValue={setPw} class="input mono" />
          </Field>
        </Modal>
      )}
      {dialog}
    </Modal>
  );
}

type Mode = 'none' | 'any' | 'selected';

function AccessEditor({ userId }: { userId: number }) {
  const [pbxs, setPbxs] = useState<PBX[] | null>(null);
  const [phones, setPhones] = useState<AdminPhone[]>([]);
  const [rows, setRows] = useState<Record<number, { mode: Mode; dialRules: string; phoneIds: number[] }>>({});
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    Promise.all([get<PBX[]>('/admin/pbxs'), get<AdminPhone[]>('/admin/phones'), get<Access[]>(`/admin/users/${userId}/access`)])
      .then(([p, ph, acc]) => {
        setPbxs(p);
        setPhones(ph.filter((x) => x.ownerId === 0));
        const r: typeof rows = {};
        for (const x of p) r[x.id] = { mode: 'none', dialRules: '', phoneIds: [] };
        for (const a of acc) r[a.pbxId] = { mode: a.mode, dialRules: a.dialRules, phoneIds: a.phoneIds };
        setRows(r);
      })
      .catch(toastError);
  }, [userId]);

  if (!pbxs) return <div class="muted">Loading access…</div>;
  const set = (id: number, patchRow: Partial<(typeof rows)[number]>) => {
    setRows({ ...rows, [id]: { ...rows[id], ...patchRow } });
    setDirty(true);
  };
  const save = async () => {
    const body = Object.entries(rows)
      .filter(([, r]) => r.mode !== 'none')
      .map(([id, r]) => ({ pbxId: Number(id), mode: r.mode, dialRules: r.dialRules, phoneIds: r.phoneIds }));
    await put(`/admin/users/${userId}/access`, body);
    setDirty(false);
    toast('Access saved', 'success');
  };

  return (
    <div class="stack">
      <div class="row between">
        <h2 style={{ margin: 0 }}>Access to PBXs</h2>
        <AsyncButton class="primary" disabled={!dirty} onClick={save}>
          Save access
        </AsyncButton>
      </div>
      {pbxs.length === 0 && <div class="muted">No PBXs yet. Add one under “PBXs & extensions”.</div>}
      {pbxs.map((p) => {
        const r = rows[p.id];
        const exts = phones.filter((x) => x.pbxId === p.id);
        return (
          <div key={p.id} class="card" style={{ boxShadow: 'none', background: 'var(--surface-2)' }}>
            <div class="row between">
              <b>
                {p.name} {!p.enabled && <span class="badge">disabled</span>}
              </b>
              <div class="segmented" role="radiogroup" aria-label={'Access to ' + p.name}>
                {(
                  [
                    ['none', 'No access'],
                    ['selected', 'Selected extensions'],
                    ['any', 'Any SIP account'],
                  ] as [Mode, string][]
                ).map(([m, label]) => (
                  <button key={m} type="button" class={r.mode === m ? 'on' : ''} onClick={() => set(p.id, { mode: m })}>
                    {label}
                  </button>
                ))}
              </div>
            </div>
            {r.mode !== 'none' && (
              <div class="stack" style={{ marginTop: '12px' }}>
                {r.mode === 'any' && (
                  <p class="small muted" style={{ margin: 0 }}>
                    The user may add phones on this PBX with any SIP username and password they know. They can also use the extensions ticked below.
                  </p>
                )}
                <div>
                  <div class="small muted" style={{ marginBottom: '6px' }}>
                    Extensions this user may use:
                  </div>
                  {exts.length === 0 ? (
                    <div class="small muted">No extensions defined on this PBX yet.</div>
                  ) : (
                    <div class="row" style={{ gap: '6px 18px' }}>
                      {exts.map((x) => (
                        <Check
                          key={x.id}
                          checked={r.phoneIds.includes(x.id)}
                          onChange={(v) => set(p.id, { phoneIds: v ? [...r.phoneIds, x.id] : r.phoneIds.filter((i) => i !== x.id) })}
                          label={
                            <>
                              <b>{x.sipUser}</b> <span class="muted">{x.label || x.displayName}</span>
                            </>
                          }
                        />
                      ))}
                    </div>
                  )}
                </div>
                <Field
                  label="Dial rules (optional)"
                  hint={
                    <>
                      One per line; empty = the PBX decides. <code>1001</code> exact, <code>_1XXX</code> pattern (X=0-9, Z=1-9, N=2-9,{' '}
                      <code>[1-5]</code>, <code>.</code> = one or more), <code>-_900.</code> denies. Example: <code>_1XXX</code> only internal calls.
                    </>
                  }
                >
                  <textarea
                    class="input"
                    rows={3}
                    value={r.dialRules}
                    placeholder={'_1XXX\n_0NXXXXXXXXX\n-_0900.'}
                    onInput={(e) => set(p.id, { dialRules: (e.target as HTMLTextAreaElement).value })}
                  />
                </Field>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
