import { useEffect, useState } from 'preact/hooks';
import { del, get, post, put, type PBX, type RegState } from '../api';
import { toast, toastError } from '../store';
import { AsyncButton, Check, Field, Input, Modal, RegBadge, useConfirm } from '../components/ui';
import { PhoneForm } from '../views/Settings';

interface AdminPhone {
  id: number;
  pbxId: number;
  ownerId: number;
  label: string;
  sipUser: string;
  authUser: string;
  displayName: string;
  register: boolean;
  pbxName: string;
  ownerName: string;
  reg: RegState;
  onlineCount: number;
}

export function PbxAdmin() {
  const [pbxs, setPbxs] = useState<PBX[] | null>(null);
  const [phones, setPhones] = useState<AdminPhone[]>([]);
  const [editPbx, setEditPbx] = useState<PBX | 'new' | null>(null);
  const [editPhone, setEditPhone] = useState<{ pbx: PBX; phone?: AdminPhone } | null>(null);
  const { confirm, dialog } = useConfirm();
  const load = () =>
    Promise.all([get<PBX[]>('/admin/pbxs'), get<AdminPhone[]>('/admin/phones')])
      .then(([p, ph]) => {
        setPbxs(p);
        setPhones(ph);
      })
      .catch(toastError);
  useEffect(() => {
    load();
  }, []);
  if (!pbxs) return <div class="muted">Loading…</div>;

  return (
    <>
      <div class="row between" style={{ marginBottom: '14px' }}>
        <p class="muted" style={{ margin: 0 }}>
          The server connects to these PBXs; clients never talk SIP themselves.
        </p>
        <button class="btn primary" onClick={() => setEditPbx('new')}>
          Add PBX
        </button>
      </div>
      {pbxs.length === 0 && (
        <div class="card empty">
          No PBX yet. Add your PBX (for example FreePBX / Asterisk on your LAN) to get started.
        </div>
      )}
      {pbxs.map((p) => {
        const list = phones.filter((x) => x.pbxId === p.id);
        return (
          <div class="card pad-0" key={p.id} style={{ marginBottom: '16px' }}>
            <div class="card-head" style={{ padding: '18px 18px 0' }}>
              <div>
                <h2>
                  {p.name} {!p.enabled && <span class="badge">disabled</span>}
                </h2>
                <div class="small muted mono">
                  {p.transport}://{p.host}:{p.port}
                  {p.domain && ` · domain ${p.domain}`} · {p.codecs.join(', ')} · DTMF {p.dtmfMode}
                </div>
              </div>
              <div class="row">
                <AsyncButton
                  class="small"
                  onClick={async () => {
                    const r = await post<{ ok: boolean; rttMs?: number; status?: number; error?: string }>(`/admin/pbxs/${p.id}/ping`);
                    if (r.ok) toast(`${p.name} answered (${r.status}) in ${r.rttMs?.toFixed(1)} ms`, 'success');
                    else toast(`${p.name}: ${r.error}`, 'error');
                  }}
                >
                  Test connection
                </AsyncButton>
                <button class="btn small" onClick={() => setEditPbx(p)}>
                  Edit
                </button>
                <button
                  class="btn small danger"
                  onClick={() =>
                    confirm(`Delete PBX ${p.name}? All its extensions and user phones are deleted and calls on it end.`, async () => {
                      await del('/admin/pbxs/' + p.id);
                      load();
                    })
                  }
                >
                  Delete
                </button>
              </div>
            </div>
            <div class="row between" style={{ padding: '14px 18px 8px' }}>
              <h3 style={{ margin: 0 }}>Extensions</h3>
              <button class="btn small primary" onClick={() => setEditPhone({ pbx: p })}>
                Add extension
              </button>
            </div>
            {list.length === 0 ? (
              <div class="empty small">No extensions. Add the extensions users may use, with their SIP passwords.</div>
            ) : (
              <div class="table-wrap">
                <table class="table">
                  <thead>
                    <tr>
                      <th>Extension</th>
                      <th>Label</th>
                      <th>Owner</th>
                      <th>Registration</th>
                      <th>Online</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {list.map((x) => (
                      <tr key={x.id}>
                        <td>
                          <b>{x.sipUser}</b>
                        </td>
                        <td>{x.label || x.displayName || <span class="muted">–</span>}</td>
                        <td>{x.ownerId ? <span class="badge">{x.ownerName}'s own</span> : <span class="badge info">Shared</span>}</td>
                        <td><RegBadge status={x.reg.status} error={x.reg.error} register={x.register} /></td>
                        <td>{x.onlineCount || <span class="muted">–</span>}</td>
                        <td class="nowrap" style={{ textAlign: 'right' }}>
                          <AsyncButton
                            class="small"
                            onClick={async () => {
                              await post(`/admin/phones/${x.id}/test`);
                              toast(`${x.sipUser}: credentials OK`, 'success');
                            }}
                          >
                            Test
                          </AsyncButton>{' '}
                          {!x.ownerId && (
                            <button class="btn small" onClick={() => setEditPhone({ pbx: p, phone: x })}>
                              Edit
                            </button>
                          )}{' '}
                          <button
                            class="btn small danger"
                            onClick={() =>
                              confirm(`Delete extension ${x.sipUser}${x.ownerId ? ` (owned by ${x.ownerName})` : ''}?`, async () => {
                                await del('/admin/phones/' + x.id);
                                load();
                              })
                            }
                          >
                            Delete
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        );
      })}
      {editPbx && <PbxForm pbx={editPbx === 'new' ? undefined : editPbx} onClose={() => setEditPbx(null)} onSaved={load} />}
      {editPhone && (
        <PhoneForm
          title={(editPhone.phone ? 'Edit extension' : 'Add extension') + ' · ' + editPhone.pbx.name}
          initial={editPhone.phone ?? { pbxId: editPhone.pbx.id }}
          onClose={() => setEditPhone(null)}
          onSave={async (body) => {
            if (editPhone.phone) await put('/admin/phones/' + editPhone.phone.id, body);
            else await post(`/admin/pbxs/${editPhone.pbx.id}/phones`, body);
            toast('Saved', 'success');
            load();
          }}
        />
      )}
      {dialog}
    </>
  );
}

function PbxForm(props: { pbx?: PBX; onClose: () => void; onSaved: () => void }) {
  const p = props.pbx;
  const [name, setName] = useState(p?.name ?? '');
  const [host, setHost] = useState(p?.host ?? '');
  const [port, setPort] = useState(String(p?.port ?? 5060));
  const [transport, setTransport] = useState<PBX['transport']>(p?.transport ?? 'udp');
  const [domain, setDomain] = useState(p?.domain ?? '');
  const [tlsVerify, setTlsVerify] = useState(p?.tlsVerify ?? true);
  const [codecs, setCodecs] = useState<string[]>(p?.codecs ?? ['PCMU', 'PCMA']);
  const [dtmf, setDtmf] = useState<PBX['dtmfMode']>(p?.dtmfMode ?? 'rfc4733');
  const [expiry, setExpiry] = useState(String(p?.registerExpiry ?? 300));
  const [enabled, setEnabled] = useState(p?.enabled ?? true);
  const [grantMe, setGrantMe] = useState(true);
  const [error, setError] = useState('');

  const save = async () => {
    setError('');
    const body = {
      name: name.trim(), host: host.trim(), port: Number(port), transport, domain: domain.trim(), tlsVerify, codecs, dtmfMode: dtmf,
      registerExpiry: Number(expiry), enabled, ...(p ? {} : { grantMe }),
    };
    try {
      if (p) await put('/admin/pbxs/' + p.id, body);
      else await post('/admin/pbxs', body);
      toast('PBX saved', 'success');
      props.onSaved();
      props.onClose();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <Modal
      title={p ? 'Edit PBX' : 'Add PBX'}
      onClose={props.onClose}
      footer={
        <>
          <button class="btn" onClick={props.onClose}>
            Cancel
          </button>
          <AsyncButton class="primary" onClick={save}>
            Save
          </AsyncButton>
        </>
      }
    >
      <form class="stack" onSubmit={(e) => (e.preventDefault(), save())}>
        <div class="form-grid">
          <Field label="Name">
            <Input value={name} onValue={setName} required maxLength={64} placeholder="Office FreePBX" />
          </Field>
          <Field label="Host" hint="IP or host name, as seen from this server">
            <Input value={host} onValue={setHost} required placeholder="192.168.1.10" />
          </Field>
          <Field label="Transport">
            <select
              class="input"
              value={transport}
              onChange={(e) => {
                const t = (e.target as HTMLSelectElement).value as PBX['transport'];
                setTransport(t);
                if (t === 'tls' && port === '5060') setPort('5061');
                if (t !== 'tls' && port === '5061') setPort('5060');
              }}
            >
              <option value="udp">UDP</option>
              <option value="tcp">TCP</option>
              <option value="tls">TLS</option>
            </select>
          </Field>
          <Field label="Port">
            <Input value={port} onValue={setPort} inputMode="numeric" required />
          </Field>
          <Field label="SIP domain" hint="Only if the PBX expects a domain other than its host">
            <Input value={domain} onValue={setDomain} placeholder="(same as host)" />
          </Field>
          <Field label="Registration interval (s)">
            <Input value={expiry} onValue={setExpiry} inputMode="numeric" />
          </Field>
          <Field label="Codecs (preference order)">
            <select class="input" value={codecs.join(',')} onChange={(e) => setCodecs((e.target as HTMLSelectElement).value.split(','))}>
              <option value="PCMU,PCMA">G.711 µ-law, then A-law</option>
              <option value="PCMA,PCMU">G.711 A-law, then µ-law</option>
              <option value="PCMU">G.711 µ-law only</option>
              <option value="PCMA">G.711 A-law only</option>
            </select>
          </Field>
          <Field label="DTMF">
            <select class="input" value={dtmf} onChange={(e) => setDtmf((e.target as HTMLSelectElement).value as PBX['dtmfMode'])}>
              <option value="rfc4733">RFC 4733 (RTP events)</option>
              <option value="info">SIP INFO</option>
            </select>
          </Field>
        </div>
        {transport === 'tls' && (
          <Check checked={tlsVerify} onChange={setTlsVerify} label="Verify the PBX's TLS certificate" hint="Turn off only for a self-signed PBX certificate on a trusted network." />
        )}
        <Check checked={enabled} onChange={setEnabled} label="Enabled" />
        {!p && <Check checked={grantMe} onChange={setGrantMe} label="Give me access to this PBX (any SIP account)" />}
        {error && <div class="error-text">{error}</div>}
        <button class="hidden" />
      </form>
    </Modal>
  );
}
