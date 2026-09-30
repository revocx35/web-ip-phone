import { useStore, toastError } from '../store';
import { phone, phoneState } from '../phone/client';
import { IconPhone } from '../components/icons';
import { Initials } from './Phone';

/** Full-screen incoming call prompt, shown on every page. */
export function IncomingCallModal() {
  const ps = useStore(phoneState);
  const call = ps.calls.find((c) => c.state === 'incoming' && !c.mine);
  if (!call) return null;
  const busy = ps.calls.find((c) => c.mine && c.attached && c.state !== 'ended');
  const ph = ps.phones.find((p) => p.id === call.phoneId);
  const name = call.remoteName || call.remote;

  const answer = async () => {
    try {
      if (busy) await phone.hangup(busy.id);
      await phone.answer(call.id);
    } catch (e) {
      toastError(e);
    }
  };

  return (
    <div class="backdrop">
      <div class="modal incoming" role="alertdialog" aria-label={'Incoming call from ' + name} style={{ maxWidth: '380px' }}>
        <div class="small muted">Incoming call{ph ? ' · ' + (ph.label || ph.sipUser) : ''}</div>
        <div class="avatar" style={{ margin: '18px auto 12px' }}>
          <Initials name={name} />
        </div>
        <div style={{ fontSize: '1.4rem', fontWeight: 650 }} class="ellipsis">
          {name}
        </div>
        {call.remoteName && <div class="muted">{call.remote}</div>}
        {busy && <div class="banner warn" style={{ marginTop: '12px' }}>Answering ends your current call.</div>}
        <div class="actions">
          <div>
            <button class="call-btn hang" aria-label="Decline" onClick={() => phone.hangup(call.id).catch(toastError)}>
              <IconPhone />
            </button>
            <div class="lbl">Decline</div>
          </div>
          <div>
            <button class="call-btn" aria-label="Answer" onClick={answer}>
              <IconPhone />
            </button>
            <div class="lbl">Answer</div>
          </div>
        </div>
      </div>
    </div>
  );
}
