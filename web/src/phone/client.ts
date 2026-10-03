// WebSocket connection to the server and the client-side call logic.
import type { CallView, Me, PhoneView } from '../api';
import { Store, toast } from '../store';
import { callerName, clearContacts, loadContacts } from '../contacts';
import { audio } from './audio';

export interface PhoneState {
  status: 'connecting' | 'online' | 'offline' | 'closed';
  closedReason?: string;
  phones: PhoneView[];
  calls: CallView[];
  selected: number | null;
  version?: string;
}

export const phoneState = new Store<PhoneState>({ status: 'offline', phones: [], calls: [], selected: null });

const FRAME_AUDIO = 1;

function selectedKey(userId: number) {
  return 'wip.phone.' + userId;
}

class PhoneClient {
  private ws?: WebSocket;
  private user?: Me;
  private stopped = true;
  private retry = 0;
  private retryTimer?: number;
  private seq = 0;
  private pending = new Map<string, { resolve: (v: any) => void; reject: (e: Error) => void; timer: number }>();
  private audioCall?: string; // call whose audio runs locally
  private toneFor?: string;
  private endedTimers = new Map<string, number>();

  start(user: Me) {
    if (!this.stopped && this.user?.id === user.id) return;
    this.stop();
    this.user = user;
    this.stopped = false;
    let sel: number | null = null;
    try {
      const v = localStorage.getItem(selectedKey(user.id));
      sel = v ? Number(v) : null;
    } catch {
      /* ignore */
    }
    phoneState.set({ status: 'connecting', phones: [], calls: [], selected: sel });
    this.connect();
  }

  stop() {
    this.stopped = true;
    clearTimeout(this.retryTimer);
    this.ws?.close();
    this.ws = undefined;
    this.stopAudio();
    audio.stopTone();
    clearContacts();
    phoneState.set({ status: 'offline', phones: [], calls: [], selected: null });
  }

  private connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/api/v1/ws`);
    ws.binaryType = 'arraybuffer';
    this.ws = ws;
    ws.onmessage = (e) => {
      if (typeof e.data === 'string') {
        try {
          this.onMessage(JSON.parse(e.data));
        } catch (err) {
          console.error('bad message', err);
        }
        return;
      }
      const b = new Uint8Array(e.data as ArrayBuffer);
      if (b.length > 1 && b[0] === FRAME_AUDIO && this.audioCall) audio.play(b.slice(1));
    };
    ws.onclose = (e) => {
      if (this.ws !== ws) return;
      this.ws = undefined;
      for (const [, p] of this.pending) {
        clearTimeout(p.timer);
        p.reject(new Error('Connection lost'));
      }
      this.pending.clear();
      if (this.stopped) return;
      if (e.code === 1008) {
        // policy: session ended / signed out elsewhere
        phoneState.set((s) => ({ ...s, status: 'closed', closedReason: e.reason || 'Signed out' }));
        this.stopAudio();
        window.dispatchEvent(new CustomEvent('wip-session-ended', { detail: e.reason }));
        return;
      }
      phoneState.set((s) => ({ ...s, status: 'connecting' }));
      const delay = Math.min(15000, 500 * 2 ** this.retry++) * (0.75 + Math.random() * 0.5);
      this.retryTimer = window.setTimeout(() => this.connect(), delay);
    };
  }

  private onMessage(m: any) {
    switch (m.type) {
      case 'hello': {
        this.retry = 0;
        const phones: PhoneView[] = m.phones;
        let selected = phoneState.get().selected;
        if (selected == null || !phones.some((p) => p.id === selected)) selected = phones[0]?.id ?? null;
        phoneState.set((s) => ({ ...s, status: 'online', phones, calls: m.calls, selected, version: m.version }));
        this.goOnline(selected);
        this.reconcile();
        loadContacts(); // also catches changes made while this connection was down
        break;
      }
      case 'phones': {
        const phones: PhoneView[] = m.phones;
        const prev = phoneState.get().selected;
        let selected = prev;
        if (selected == null || !phones.some((p) => p.id === selected)) selected = phones[0]?.id ?? null;
        phoneState.set((s) => ({ ...s, phones, selected }));
        // A phone was granted or removed while connected: go online with the new choice.
        if (selected !== prev || (selected != null && !phones.find((p) => p.id === selected)?.online)) this.goOnline(selected);
        break;
      }
      case 'call':
        this.upsertCall(m.call);
        break;
      case 'contacts':
        loadContacts();
        break;
      case 'ack':
      case 'error': {
        const p = m.req ? this.pending.get(m.req) : undefined;
        if (p) {
          clearTimeout(p.timer);
          this.pending.delete(m.req);
          if (m.type === 'ack') p.resolve(m);
          else p.reject(new Error(m.error));
        } else if (m.type === 'error') {
          toast(m.error, 'error');
        }
        break;
      }
    }
  }

  private upsertCall(c: CallView) {
    phoneState.set((s) => {
      const calls = s.calls.filter((x) => x.id !== c.id);
      calls.push(c);
      return { ...s, calls };
    });
    if (c.state === 'ended' && !this.endedTimers.has(c.id)) {
      // Keep the ended call visible for a moment ("Call ended").
      this.endedTimers.set(
        c.id,
        window.setTimeout(() => {
          this.endedTimers.delete(c.id);
          phoneState.set((s) => ({ ...s, calls: s.calls.filter((x) => x.id !== c.id) }));
        }, 2500),
      );
      if (c.mine && c.endStatus && !['answered', 'cancelled', 'answered_elsewhere', 'rejected'].includes(c.endStatus) && c.direction === 'out') {
        toast(`Call to ${callerName(c.remote)}: ${c.endReason || c.endStatus}`, 'error');
      }
    }
    this.reconcile();
  }

  /** Starts/stops local audio and tones to match the call states. */
  private reconcile() {
    const calls = phoneState.get().calls;
    const mine = calls.find((c) => c.attached && c.state !== 'ended');
    if (mine && (mine.state === 'active' || mine.state === 'early')) {
      if (this.audioCall !== mine.id) {
        this.audioCall = mine.id;
        audio
          .startCall(mine.codec, (f) => this.sendAudio(f))
          .catch((e) => {
            toast('Microphone unavailable: ' + (e?.message ?? e), 'error', 8000);
            this.audioCall = mine.id; // keep receiving audio even without a microphone
          });
      } else {
        audio.setCodec(mine.codec);
      }
    } else if (this.audioCall && (!mine || mine.id !== this.audioCall)) {
      this.stopAudio();
    }

    // tones
    const incoming = calls.find((c) => c.state === 'incoming' && !c.mine);
    let tone: string | undefined;
    if (mine && mine.state === 'ringing') tone = 'ringback:' + mine.id;
    else if (incoming && !mine) tone = 'ring:' + incoming.id;
    if (tone !== this.toneFor) {
      this.toneFor = tone;
      audio.stopTone();
      if (tone?.startsWith('ringback')) audio.ringback();
      else if (tone?.startsWith('ring:')) {
        audio.ringtone();
        this.notify(incoming!);
      }
    }
  }

  private notify(c: CallView) {
    if (!document.hidden || typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
    const name = callerName(c.remote, c.remoteName);
    const n = new Notification('Incoming call', {
      body: name !== c.remote ? `${name} (${c.remote})` : c.remote,
      tag: 'call-' + c.id,
      requireInteraction: true,
    });
    n.onclick = () => {
      window.focus();
      n.close();
    };
  }

  private stopAudio() {
    if (this.audioCall) {
      this.audioCall = undefined;
      audio.stopCall();
    }
  }

  private sendAudio(f: Uint8Array) {
    const ws = this.ws;
    if (!ws || ws.readyState !== WebSocket.OPEN || this.muted) return;
    if (ws.bufferedAmount > 16000) return; // ~1 s backlog: drop rather than lag
    const buf = new Uint8Array(1 + f.length);
    buf[0] = FRAME_AUDIO;
    buf.set(f, 1);
    ws.send(buf);
  }

  muted = false;

  private send(m: object) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m));
  }

  request<T = any>(m: object): Promise<T> {
    return new Promise((resolve, reject) => {
      if (this.ws?.readyState !== WebSocket.OPEN) {
        reject(new Error('Not connected to the server'));
        return;
      }
      const req = 'r' + ++this.seq;
      const timer = window.setTimeout(() => {
        this.pending.delete(req);
        reject(new Error('The server did not answer'));
      }, 20000);
      this.pending.set(req, { resolve, reject, timer });
      this.send({ ...m, req });
    });
  }

  goOnline(phoneId: number | null) {
    this.send({ type: 'online', phones: phoneId == null ? [] : [phoneId] });
  }

  select(phoneId: number) {
    phoneState.set((s) => ({ ...s, selected: phoneId }));
    try {
      if (this.user) localStorage.setItem(selectedKey(this.user.id), String(phoneId));
    } catch {
      /* ignore */
    }
    this.goOnline(phoneId);
  }

  async dial(number: string) {
    const s = phoneState.get();
    if (s.selected == null) throw new Error('Choose a phone first');
    await audio.unlock();
    this.muted = false;
    return this.request<{ call: string }>({ type: 'dial', phone: s.selected, number });
  }

  async answer(id: string) {
    await audio.unlock();
    this.muted = false;
    return this.request({ type: 'answer', call: id });
  }

  reject(id: string) {
    return this.request({ type: 'reject', call: id });
  }

  hangup(id: string) {
    return this.request({ type: 'hangup', call: id });
  }

  dtmf(id: string, digits: string) {
    audio.keyTone(digits);
    return this.request({ type: 'dtmf', call: id, digits });
  }

  hold(id: string, on: boolean) {
    return this.request({ type: 'hold', call: id, on });
  }

  transfer(id: string, number: string) {
    return this.request({ type: 'transfer', call: id, number });
  }

  async attach(id: string) {
    await audio.unlock();
    return this.request({ type: 'attach', call: id });
  }

  setMuted(m: boolean) {
    this.muted = m;
  }
}

export const phone = new PhoneClient();
