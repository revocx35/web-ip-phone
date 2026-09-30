import { useEffect, useState } from 'preact/hooks';
import { del, get, put, type AuditEntry, type CallRecord, type Me, type Settings } from '../api';
import { navigate, route, toast, toastError, useStore } from '../store';
import { AsyncButton, Field, fmtDuration, fmtTime, useInterval } from '../components/ui';
import { UsersAdmin } from './Users';
import { PbxAdmin } from './Pbxs';

const TABS: [string, string][] = [
  ['', 'Overview'],
  ['users', 'Users'],
  ['pbxs', 'PBXs & extensions'],
  ['calls', 'Call log'],
  ['audit', 'Audit log'],
  ['settings', 'Policies'],
];

export function AdminView({ me }: { me: Me }) {
  const path = useStore(route);
  const tab = path.split('/')[2] ?? '';
  return (
    <>
      <h1>Administration</h1>
      <div class="tabs" role="tablist">
        {TABS.map(([id, label]) => (
          <button key={id} role="tab" class={tab === id ? 'on' : ''} onClick={() => navigate('/admin' + (id ? '/' + id : ''))}>
            {label}
          </button>
        ))}
      </div>
      {tab === '' && <Overview />}
      {tab === 'users' && <UsersAdmin me={me} />}
      {tab === 'pbxs' && <PbxAdmin />}
      {tab === 'calls' && <CallLog />}
      {tab === 'audit' && <AuditLog />}
      {tab === 'settings' && <PolicySettings />}
    </>
  );
}

interface ActiveCall {
  id: string;
  direction: string;
  remote: string;
  remoteName: string;
  state: string;
  startedAt: string;
  answeredAt?: string;
  username: string;
  phoneLabel: string;
  pbxName: string;
  devices: number;
}

interface Status {
  version: string;
  uptimeSeconds: number;
  activeCalls: ActiveCall[];
  connections: Record<string, number>;
  certFingerprint: string;
  sipPort: number;
  rtpPorts: string;
}

function Overview() {
  const [st, setSt] = useState<Status | null>(null);
  const [regs, setRegs] = useState<{ registered: number; failed: number; total: number } | null>(null);
  const load = async () => {
    try {
      setSt(await get<Status>('/admin/status'));
      const phones = await get<{ reg: { status: string } }[]>('/admin/phones');
      setRegs({
        total: phones.length,
        registered: phones.filter((p) => p.reg.status === 'registered').length,
        failed: phones.filter((p) => p.reg.status === 'failed').length,
      });
    } catch (e) {
      toastError(e);
    }
  };
  useEffect(() => {
    load();
  }, []);
  useInterval(load, 4000);
  if (!st) return <div class="muted">Loading…</div>;
  const users = Object.keys(st.connections).length;
  const devices = Object.values(st.connections).reduce((a, b) => a + b, 0);
  return (
    <>
      <div class="form-grid">
        <div class="card">
          <div class="muted small">Active calls</div>
          <div style={{ fontSize: '2rem', fontWeight: 700 }}>{st.activeCalls.length}</div>
        </div>
        <div class="card">
          <div class="muted small">Online users / devices</div>
          <div style={{ fontSize: '2rem', fontWeight: 700 }}>
            {users} / {devices}
          </div>
        </div>
        <div class="card">
          <div class="muted small">Registered phones</div>
          <div style={{ fontSize: '2rem', fontWeight: 700 }}>
            {regs ? `${regs.registered} / ${regs.total}` : '–'}
            {regs && regs.failed > 0 && <span class="badge bad" style={{ marginLeft: '10px', verticalAlign: 'middle' }}>{regs.failed} failed</span>}
          </div>
        </div>
      </div>
      <div class="card pad-0 section">
        <div class="card-head" style={{ padding: '18px 18px 0' }}>
          <h2>Calls in progress</h2>
        </div>
        {st.activeCalls.length === 0 ? (
          <div class="empty">No calls right now</div>
        ) : (
          <div class="table-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th>User</th>
                  <th>Phone</th>
                  <th>Direction</th>
                  <th>Other party</th>
                  <th>State</th>
                  <th>Duration</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {st.activeCalls.map((c) => (
                  <tr key={c.id}>
                    <td>{c.username || <span class="muted">ringing ({c.devices} devices)</span>}</td>
                    <td>
                      {c.phoneLabel} <span class="muted small">@ {c.pbxName}</span>
                    </td>
                    <td>{c.direction === 'in' ? 'Incoming' : 'Outgoing'}</td>
                    <td>{c.remoteName ? `${c.remoteName} (${c.remote})` : c.remote}</td>
                    <td>{c.state}</td>
                    <td class="mono">{c.answeredAt ? fmtDuration((Date.now() - Date.parse(c.answeredAt)) / 1000) : '–'}</td>
                    <td>
                      <AsyncButton class="small danger" onClick={async () => (await del('/admin/active-calls/' + c.id), load())}>
                        Hang up
                      </AsyncButton>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <div class="card section">
        <h2>Server</h2>
        <dl class="kv">
          <dt>Version</dt>
          <dd>{st.version}</dd>
          <dt>Uptime</dt>
          <dd>{fmtDuration(st.uptimeSeconds)}</dd>
          <dt>SIP port</dt>
          <dd>{st.sipPort} (UDP/TCP, towards your PBXs only)</dd>
          <dt>RTP ports</dt>
          <dd>{st.rtpPorts} (UDP)</dd>
          {st.certFingerprint && (
            <>
              <dt>TLS certificate</dt>
              <dd>
                <span class="small muted">SHA-256 fingerprint (compare it in the Android app when it asks you to trust this server):</span>
                <div class="mono small" style={{ wordBreak: 'break-all' }}>{st.certFingerprint}</div>
              </dd>
            </>
          )}
        </dl>
      </div>
    </>
  );
}

function CallLog() {
  const [calls, setCalls] = useState<CallRecord[] | null>(null);
  useEffect(() => {
    get<CallRecord[]>('/admin/calls?limit=500').then(setCalls).catch(toastError);
  }, []);
  if (!calls) return <div class="muted">Loading…</div>;
  return (
    <div class="card pad-0">
      {calls.length === 0 ? (
        <div class="empty">No calls yet</div>
      ) : (
        <div class="table-wrap">
          <table class="table small">
            <thead>
              <tr>
                <th>Time</th>
                <th>User</th>
                <th>Phone</th>
                <th>Dir.</th>
                <th>Other party</th>
                <th>Result</th>
                <th>Duration</th>
              </tr>
            </thead>
            <tbody>
              {calls.map((c) => (
                <tr key={c.id}>
                  <td class="nowrap">{fmtTime(c.startedAt)}</td>
                  <td>{c.username || <span class="muted">–</span>}</td>
                  <td>
                    {c.phoneLabel} <span class="muted">@ {c.pbxName}</span>
                  </td>
                  <td>{c.direction === 'in' ? 'In' : 'Out'}</td>
                  <td>{c.remoteName ? `${c.remoteName} (${c.remote})` : c.remote}</td>
                  <td title={c.reason}>
                    <span class={'badge ' + (c.status === 'answered' ? 'ok' : c.status === 'missed' || c.status === 'failed' ? 'bad' : '')}>{c.status}</span>
                  </td>
                  <td class="mono">{c.answeredAt && c.endedAt ? fmtDuration((Date.parse(c.endedAt) - Date.parse(c.answeredAt)) / 1000) : '–'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function AuditLog() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [more, setMore] = useState(true);
  const load = async (before = 0) => {
    const list = await get<AuditEntry[]>(`/admin/audit?limit=100${before ? '&before=' + before : ''}`);
    setEntries((e) => (before ? [...e, ...list] : list));
    setMore(list.length === 100);
  };
  useEffect(() => {
    load().catch(toastError);
  }, []);
  return (
    <div class="card pad-0">
      <div class="table-wrap">
        <table class="table small">
          <thead>
            <tr>
              <th>Time</th>
              <th>User</th>
              <th>IP</th>
              <th>Action</th>
              <th>Target</th>
              <th>Details</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id}>
                <td class="nowrap">{fmtTime(e.time)}</td>
                <td>{e.username || <span class="muted">–</span>}</td>
                <td class="mono">{e.ip}</td>
                <td>
                  <span class={'badge ' + (e.success ? '' : 'bad')}>{e.action}</span>
                </td>
                <td>{e.target}</td>
                <td class="muted">{e.details}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {more && entries.length > 0 && (
        <div class="row" style={{ justifyContent: 'center', padding: '12px' }}>
          <AsyncButton onClick={() => load(entries[entries.length - 1].id)}>Load more</AsyncButton>
        </div>
      )}
    </div>
  );
}

function PolicySettings() {
  const [s, setS] = useState<Settings | null>(null);
  useEffect(() => {
    get<Settings>('/admin/settings').then(setS).catch(toastError);
  }, []);
  if (!s) return <div class="muted">Loading…</div>;
  const num = (key: keyof Settings, label: string, hint?: string) => (
    <Field label={label} hint={hint}>
      <input
        class="input"
        type="number"
        value={String(s[key])}
        onInput={(e) => setS({ ...s, [key]: Number((e.target as HTMLInputElement).value) })}
      />
    </Field>
  );
  return (
    <div class="card stack">
      <h2>Security policies</h2>
      <Field label="Two-factor authentication" hint="Users without 2FA who fall under the policy must set it up at their next sign-in.">
        <select class="input" value={s.require2fa} onChange={(e) => setS({ ...s, require2fa: (e.target as HTMLSelectElement).value as Settings['require2fa'] })}>
          <option value="off">Optional for everyone</option>
          <option value="admins">Required for administrators</option>
          <option value="all">Required for everyone</option>
        </select>
      </Field>
      <div class="form-grid">
        {num('passwordMinLength', 'Minimum password length', '8–64')}
        {num('maxCallsPerUser', 'Simultaneous calls per user', '1–10')}
        {num('webSessionIdleMinutes', 'Browser: sign out after idle (minutes)')}
        {num('webSessionMaxHours', 'Browser: maximum session (hours)')}
        {num('appSessionIdleDays', 'App: sign out after idle (days)')}
        {num('appSessionMaxDays', 'App: maximum session (days)')}
        {num('callHistoryDays', 'Keep call history (days)')}
        {num('auditLogDays', 'Keep audit log (days)', 'At least 30')}
      </div>
      <div class="row end">
        <AsyncButton
          class="primary"
          onClick={async () => {
            setS(await put<Settings>('/admin/settings', s));
            toast('Saved', 'success');
          }}
        >
          Save
        </AsyncButton>
      </div>
    </div>
  );
}
