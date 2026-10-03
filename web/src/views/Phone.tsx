import { useEffect, useRef, useState } from 'preact/hooks';
import { del, get, type CallRecord, type CallView, type Me } from '../api';
import { navigate, toast, toastError, useStore } from '../store';
import { phone, phoneState } from '../phone/client';
import { audio, audioBlocked, micLevel } from '../phone/audio';
import { callerName, contacts } from '../contacts';
import { fmtDuration, fmtTime, Initials, RegBadge, useInterval, useConfirm } from '../components/ui';
import {
  IconArrowDownLeft, IconArrowUpRight, IconBackspace, IconGrid, IconMic, IconMicOff, IconPause, IconPhone, IconTransfer, IconUserPlus, IconX,
} from '../components/icons';
import { ContactForm, ContactsPanel, type ContactEdit } from './Contacts';

const KEYS: [string, string][] = [
  ['1', ''], ['2', 'ABC'], ['3', 'DEF'], ['4', 'GHI'], ['5', 'JKL'], ['6', 'MNO'],
  ['7', 'PQRS'], ['8', 'TUV'], ['9', 'WXYZ'], ['*', ''], ['0', '+'], ['#', ''],
];

const DIAL_RE = /^[0-9A-Za-z*#+._-]{1,64}$/;

function Keypad({ onKey }: { onKey: (k: string) => void }) {
  const hold = useRef<number | undefined>(undefined);
  return (
    <div class="keypad">
      {KEYS.map(([k, sub]) => (
        <button
          key={k}
          type="button"
          class="key"
          onPointerDown={() => {
            if (k === '0') hold.current = window.setTimeout(() => ((hold.current = -1), onKey('+')), 600);
          }}
          onPointerUp={() => {
            if (hold.current === -1) {
              hold.current = undefined;
              return;
            }
            clearTimeout(hold.current);
            hold.current = undefined;
            onKey(k);
          }}
          onPointerLeave={() => clearTimeout(hold.current)}
          aria-label={k}
        >
          <b>{k}</b>
          <small>{sub}</small>
        </button>
      ))}
    </div>
  );
}

function statusText(c: CallView, now: number) {
  switch (c.state) {
    case 'calling':
      return 'Calling…';
    case 'ringing':
      return 'Ringing…';
    case 'early':
      return 'Connecting…';
    case 'incoming':
      return 'Incoming call';
    case 'active':
      if (c.hold) return 'On hold';
      if (c.remoteHold) return 'Held by the other side';
      return fmtDuration((now - Date.parse(c.answeredAt ?? c.startedAt)) / 1000);
    case 'ended':
      if (c.endStatus === 'answered_elsewhere') return 'Answered on another device';
      return c.endReason && c.endReason !== 'hung up' && c.endReason !== 'remote hung up' ? `Call ended: ${c.endReason}` : 'Call ended';
  }
}

function InCall({ call }: { call: CallView }) {
  const [now, setNow] = useState(Date.now());
  const [muted, setMuted] = useState(phone.muted);
  const [pad, setPad] = useState(false);
  const [digits, setDigits] = useState('');
  const [transfer, setTransfer] = useState<string | null>(null);
  const level = useStore(micLevel);
  const book = useStore(contacts).index;
  const hit = book.lookup(call.remote);
  const name = hit?.contact.name || call.remoteName || call.remote;
  useInterval(() => setNow(Date.now()), 500);
  const live = call.state === 'active';
  const ended = call.state === 'ended';

  const toggleMute = () => {
    const m = !muted;
    phone.setMuted(m);
    setMuted(m);
  };
  const key = (k: string) => {
    setDigits((d) => (d + k).slice(-24));
    phone.dtmf(call.id, k).catch(toastError);
  };

  return (
    <div class="in-call">
      <div class="avatar">
        <Initials name={name} />
      </div>
      <div class="who ellipsis">{name}</div>
      {name !== call.remote && <div class="num">{(hit?.number.label ? hit.number.label + ' · ' : '') + call.remote}</div>}
      <div class={'status' + (live && !call.hold && !call.remoteHold ? ' live' : '')}>{statusText(call, now)}</div>
      {live && !muted && (
        <div class="level" title="Microphone level">
          <div style={{ width: Math.min(100, Math.round(Math.sqrt(level) * 260)) + '%' }} />
        </div>
      )}
      {pad && live ? (
        <>
          <div class="mono" style={{ minHeight: '1.6em', fontSize: '1.3rem', letterSpacing: '.1em' }}>
            {digits}
          </div>
          <Keypad onKey={key} />
        </>
      ) : transfer !== null ? (
        <form
          class="stack"
          style={{ maxWidth: '300px', margin: '0 auto 12px' }}
          onSubmit={async (e) => {
            e.preventDefault();
            const target = transfer.replace(/[\s()]/g, '');
            if (!DIAL_RE.test(target)) return toast('Enter a valid number', 'error');
            try {
              await phone.transfer(call.id, target);
              toast('Transferring…', 'success');
              setTransfer(null);
            } catch (err) {
              toastError(err);
            }
          }}
        >
          <input
            class="input"
            placeholder="Transfer to number or contact"
            list="wip-transfer-targets"
            value={transfer}
            autofocus
            onInput={(e) => setTransfer((e.target as HTMLInputElement).value)}
          />
          <datalist id="wip-transfer-targets">
            {book.list.flatMap((c) =>
              c.numbers.map((n) => (
                <option key={c.id + ':' + n.number} value={n.number}>
                  {c.name + (n.label ? ' · ' + n.label : '')}
                </option>
              )),
            )}
          </datalist>
          <div class="row">
            <button type="button" class="btn grow" onClick={() => setTransfer(null)}>
              Cancel
            </button>
            <button class="btn primary grow">Transfer</button>
          </div>
        </form>
      ) : (
        <div class="controls">
          <button class={'ctrl' + (muted ? ' on' : '')} onClick={toggleMute} disabled={ended}>
            {muted ? <IconMicOff /> : <IconMic />}
            {muted ? 'Unmute' : 'Mute'}
          </button>
          <button class="ctrl" onClick={() => setPad(true)} disabled={!live}>
            <IconGrid />
            Keypad
          </button>
          <button class={'ctrl' + (call.hold ? ' on' : '')} disabled={!live} onClick={() => phone.hold(call.id, !call.hold).catch(toastError)}>
            <IconPause />
            {call.hold ? 'Resume' : 'Hold'}
          </button>
          <button class="ctrl" disabled={!live} onClick={() => setTransfer('')}>
            <IconTransfer />
            Transfer
          </button>
        </div>
      )}
      <div class="call-row">
        {pad && live ? (
          <div class="row" style={{ gap: '28px' }}>
            <button class="btn ghost" onClick={() => setPad(false)}>
              Hide
            </button>
            <button class="call-btn hang" aria-label="Hang up" onClick={() => phone.hangup(call.id).catch(toastError)}>
              <IconPhone />
            </button>
            <span style={{ width: '64px' }} />
          </div>
        ) : (
          <button class="call-btn hang" aria-label="Hang up" disabled={ended} onClick={() => phone.hangup(call.id).catch(toastError)}>
            <IconPhone />
          </button>
        )}
      </div>
      {live && call.codec && <div class="small muted">{call.codec === 'PCMA' ? 'G.711 A-law' : 'G.711 µ-law'}</div>}
    </div>
  );
}

function Dialer({ disabled, number, setNumber }: { disabled: boolean; number: string; setNumber: (s: string) => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const hint = useStore(contacts).index.suggest(number);
  const dial = async () => {
    const n = number.replace(/[\s()]/g, '');
    if (!DIAL_RE.test(n)) {
      toast('Enter a number to call', 'error');
      return;
    }
    setBusy(true);
    try {
      await phone.dial(n);
    } catch (e) {
      toastError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={(e) => (e.preventDefault(), dial())}>
      <div class="display-wrap">
        <input
          ref={input}
          class="dial-display"
          value={number}
          inputMode="tel"
          autocomplete="off"
          placeholder="Enter number"
          aria-label="Number to call"
          onInput={(e) => setNumber((e.target as HTMLInputElement).value.replace(/[^0-9A-Za-z*#+._\-\s()]/g, '').slice(0, 64))}
        />
        {number && (
          <button type="button" class="btn ghost icon-btn clear" aria-label="Delete" onClick={() => setNumber(number.slice(0, -1))}>
            <IconBackspace />
          </button>
        )}
      </div>
      <div class="suggest-slot">
        {hint && (
          <button type="button" class="suggest" title="Use this number" onClick={() => setNumber(hint.number.number)}>
            <b class="ellipsis">{hint.contact.name}</b>
            <span class="muted ellipsis">{(hint.number.label ? hint.number.label + ' ' : '') + hint.number.number}</span>
          </button>
        )}
      </div>
      <Keypad
        onKey={(k) => {
          audio.keyTone(k);
          setNumber((number + k).slice(0, 64));
        }}
      />
      <div class="call-row">
        <button class="call-btn" type="submit" disabled={disabled || busy || !number} aria-label="Call">
          <IconPhone />
        </button>
      </div>
    </form>
  );
}

function Recents({ onPick, onAdd, refreshKey }: { onPick: (n: string) => void; onAdd: (e: ContactEdit) => void; refreshKey: number }) {
  const [calls, setCalls] = useState<CallRecord[] | null>(null);
  const { confirm, dialog } = useConfirm();
  const book = useStore(contacts).index;
  const load = () => get<CallRecord[]>('/calls?limit=100').then(setCalls).catch(() => setCalls([]));
  useEffect(() => {
    load();
  }, [refreshKey]);
  return (
    <>
      {calls && calls.length > 0 && (
        <div class="row end" style={{ padding: '0 14px 4px' }}>
          <button class="btn ghost small" onClick={() => confirm('Clear your call history?', async () => (await del('/calls'), load()))}>
            Clear history
          </button>
        </div>
      )}
      {calls === null ? (
        <div class="empty">Loading…</div>
      ) : calls.length === 0 ? (
        <div class="empty">No calls yet</div>
      ) : (
        <ul class="list">
          {calls.map((c) => {
            const missed = c.direction === 'in' && !c.answeredAt;
            const dur = c.answeredAt && c.endedAt ? (Date.parse(c.endedAt) - Date.parse(c.answeredAt)) / 1000 : 0;
            const hit = book.lookup(c.remote);
            const name = hit?.contact.name || c.remoteName || c.remote;
            return (
              <li key={c.id} class="recent">
                <span class={'dir ' + (missed ? 'missed' : c.direction)} title={missed ? 'Missed' : c.direction === 'in' ? 'Incoming' : 'Outgoing'}>
                  {c.direction === 'in' ? <IconArrowDownLeft /> : <IconArrowUpRight />}
                </span>
                <div class="grow">
                  <div class="ellipsis" style={{ fontWeight: 600, color: missed ? 'var(--danger)' : undefined }}>
                    {name}
                  </div>
                  <div class="small muted ellipsis">
                    {name !== c.remote ? (hit?.number.label ? hit.number.label + ' ' : '') + c.remote + ' · ' : ''}
                    {c.phoneLabel}
                    {dur > 0 ? ' · ' + fmtDuration(dur) : c.status !== 'answered' ? ' · ' + c.status : ''}
                  </div>
                </div>
                <span class="small muted nowrap">{fmtTime(c.startedAt)}</span>
                {!hit && (
                  <button
                    class="btn ghost icon-btn"
                    title="Add to contacts"
                    aria-label={'Add ' + c.remote + ' to contacts'}
                    onClick={() => onAdd({ prefill: { name: c.remoteName, number: c.remote } })}
                  >
                    <IconUserPlus />
                  </button>
                )}
                <button class="btn ghost icon-btn" title={'Call ' + c.remote} aria-label={'Call ' + c.remote} onClick={() => onPick(c.remote)}>
                  <IconPhone />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {dialog}
    </>
  );
}

type SideTab = 'recents' | 'contacts';
const SIDE_TAB_KEY = 'wip.sideTab';

/** Recent calls and the phone book, next to the dialer (below it on narrow screens). */
function SidePanel({ me, onPick, refreshKey }: { me: Me; onPick: (n: string) => void; refreshKey: number }) {
  const [tab, setTab] = useState<SideTab>(() => {
    try {
      return localStorage.getItem(SIDE_TAB_KEY) === 'contacts' ? 'contacts' : 'recents';
    } catch {
      return 'recents';
    }
  });
  const [edit, setEdit] = useState<ContactEdit | 'new' | null>(null);
  const choose = (t: SideTab) => {
    setTab(t);
    try {
      localStorage.setItem(SIDE_TAB_KEY, t);
    } catch {
      /* ignore */
    }
  };
  return (
    <div class="card pad-0 side-panel">
      <div class="tabs in-card" role="tablist">
        <button role="tab" aria-selected={tab === 'recents'} class={tab === 'recents' ? 'on' : ''} onClick={() => choose('recents')}>
          Recent calls
        </button>
        <button role="tab" aria-selected={tab === 'contacts'} class={tab === 'contacts' ? 'on' : ''} onClick={() => choose('contacts')}>
          Contacts
        </button>
      </div>
      {tab === 'recents' ? <Recents onPick={onPick} onAdd={setEdit} refreshKey={refreshKey} /> : <ContactsPanel onCall={onPick} onEdit={setEdit} />}
      {edit && <ContactForm me={me} edit={edit === 'new' ? { prefill: { name: '', number: '' } } : edit} onClose={() => setEdit(null)} />}
    </div>
  );
}

export function PhoneView({ me }: { me: Me }) {
  const ps = useStore(phoneState);
  const blocked = useStore(audioBlocked);
  const [number, setNumber] = useState('');
  const [historyKey, setHistoryKey] = useState(0);
  const selected = ps.phones.find((p) => p.id === ps.selected);
  const current = ps.calls.find((c) => c.mine && c.attached);
  const elsewhere = ps.calls.find((c) => c.mine && !c.attached && c.state !== 'ended' && c.state !== 'incoming');
  const endedCount = ps.calls.filter((c) => c.state === 'ended').length;

  useEffect(() => {
    if (endedCount > 0) setTimeout(() => setHistoryKey((k) => k + 1), 600);
  }, [endedCount]);

  useEffect(() => {
    if (typeof Notification !== 'undefined' && Notification.permission === 'default') {
      const ask = () => Notification.requestPermission().catch(() => {});
      window.addEventListener('pointerdown', ask, { once: true });
      return () => window.removeEventListener('pointerdown', ask);
    }
  }, []);

  const pick = (n: string) => {
    setNumber(n);
    if (!current) phone.dial(n).catch(toastError);
  };

  return (
    <div class="phone-layout">
      <div class="card">
        {ps.phones.length === 0 ? (
          <div class="empty">
            <p>
              <b>No phone available yet.</b>
            </p>
            <p class="small">{me.isAdmin ? 'Add a PBX and an extension in Admin, then give yourself access.' : 'Ask your administrator for access to a PBX.'}</p>
            <div class="row" style={{ justifyContent: 'center' }}>
              {me.isAdmin && (
                <button class="btn primary" onClick={() => navigate('/admin/pbxs')}>
                  Open Admin
                </button>
              )}
              <button class="btn" onClick={() => navigate('/settings/phones')}>
                My phones
              </button>
            </div>
          </div>
        ) : (
          <>
            <div class="phone-picker">
              <span class={'dot ' + (selected?.reg.status === 'registered' ? 'ok' : selected?.reg.status === 'failed' ? 'bad' : selected?.register ? 'warn' : '')} />
              <select
                aria-label="Phone"
                value={String(ps.selected ?? '')}
                disabled={!!current}
                onChange={(e) => phone.select(Number((e.target as HTMLSelectElement).value))}
              >
                {ps.phones.map((p) => (
                  <option key={p.id} value={String(p.id)}>
                    {(p.label || p.displayName || p.sipUser) + ' · ' + p.sipUser + ' @ ' + p.pbxName}
                  </option>
                ))}
              </select>
            </div>
            {selected && (
              <div class="row between small" style={{ margin: '8px 2px 0' }}>
                <RegBadge status={selected.reg.status} error={selected.reg.error} register={selected.register} />
                {selected.reg.status === 'failed' && <span class="error-text small ellipsis grow" title={selected.reg.error}>{selected.reg.error}</span>}
              </div>
            )}
            {blocked && (
              <div class="banner warn" style={{ marginTop: '12px' }}>
                <span class="grow">Click to enable sound for incoming calls.</span>
                <button class="btn small" onClick={() => audio.unlock()}>
                  Enable
                </button>
              </div>
            )}
            {elsewhere && !current && (
              <div class="banner info" style={{ marginTop: '12px' }}>
                <span class="grow">
                  Call with <b>{callerName(elsewhere.remote, elsewhere.remoteName)}</b> is on another device.
                </span>
                <button class="btn small primary" onClick={() => phone.attach(elsewhere.id).catch(toastError)}>
                  Continue here
                </button>
              </div>
            )}
            <div style={{ marginTop: '10px' }}>
              {current ? <InCall call={current} /> : <Dialer disabled={ps.status !== 'online' || !selected} number={number} setNumber={setNumber} />}
            </div>
          </>
        )}
        {ps.status === 'closed' && (
          <div class="banner bad" style={{ marginTop: '12px' }}>
            <IconX /> {ps.closedReason}
          </div>
        )}
      </div>
      <SidePanel me={me} onPick={pick} refreshKey={historyKey} />
    </div>
  );
}
