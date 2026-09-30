// Browser audio: microphone capture and playback through AudioWorklets, plus locally
// generated tones (ringtone, ringback, key clicks).
import { Store } from '../store';

export interface AudioPrefs {
  micId: string;
  speakerId: string;
  ringerId: string;
}

const PREFS_KEY = 'wip.audio';

export function loadPrefs(): AudioPrefs {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS_KEY) ?? '{}');
    return { micId: p.micId ?? '', speakerId: p.speakerId ?? '', ringerId: p.ringerId ?? '' };
  } catch {
    return { micId: '', speakerId: '', ringerId: '' };
  }
}

export function savePrefs(p: AudioPrefs) {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(p));
  } catch {
    /* private mode */
  }
}

export const micLevel = new Store(0);
export const audioStats = new Store({ buffered: 0, target: 0, underruns: 0 });
export const audioBlocked = new Store(false);

type SinkCtx = AudioContext & { setSinkId?: (id: string) => Promise<void> };

class AudioEngine {
  private ctx?: SinkCtx;
  private ready?: Promise<void>;
  private playback?: AudioWorkletNode;
  private capture?: AudioWorkletNode;
  private mic?: MediaStream;
  private micSource?: MediaStreamAudioSourceNode;
  private sink?: GainNode;
  private tone?: { stop: () => void };
  private codec = 'PCMU';
  prefs = loadPrefs();

  /** Creates/resumes the AudioContext; must first run inside a user gesture. */
  async unlock(): Promise<void> {
    if (!this.ctx) {
      this.ctx = new AudioContext({ latencyHint: 'interactive' }) as SinkCtx;
      this.ready = (async () => {
        const ctx = this.ctx!;
        const load = (url: string) =>
          Promise.race([
            ctx.audioWorklet.addModule(url),
            new Promise<never>((_, reject) => setTimeout(() => reject(new Error('the audio engine did not start (AudioWorklet)')), 8000)),
          ]);
        await load('/worklets/capture.js');
        await load('/worklets/playback.js');
        this.playback = new AudioWorkletNode(ctx, 'wip-playback', { numberOfInputs: 0, outputChannelCount: [1] });
        this.playback.port.onmessage = (e) => audioStats.set(e.data);
        this.playback.connect(ctx.destination);
        // Keeps the capture node pulled by the graph without making it audible.
        this.sink = ctx.createGain();
        this.sink.gain.value = 0;
        this.sink.connect(ctx.destination);
        if (this.prefs.speakerId) await this.applySpeaker(this.prefs.speakerId);
      })();
      const failed = this.ctx;
      this.ready.catch(() => {
        // Let the next attempt start from scratch.
        failed.close().catch(() => {});
        if (this.ctx === failed) {
          this.ctx = undefined;
          this.ready = undefined;
        }
      });
    }
    const ctx = this.ctx;
    const ready = this.ready!;
    if (ctx.state === 'suspended') {
      // resume() never settles on some systems without an output device: don't let it
      // block placing or answering a call.
      await Promise.race([ctx.resume().catch(() => {}), new Promise((r) => setTimeout(r, 1500))]);
    }
    await ready;
    audioBlocked.set(ctx.state !== 'running');
  }

  get running() {
    return this.ctx?.state === 'running';
  }

  async applySpeaker(id: string) {
    if (this.ctx?.setSinkId) {
      try {
        await this.ctx.setSinkId(id);
      } catch {
        /* device gone: stay on default */
      }
    }
  }

  setPrefs(p: AudioPrefs) {
    const speakerChanged = p.speakerId !== this.prefs.speakerId;
    this.prefs = p;
    savePrefs(p);
    if (speakerChanged) this.applySpeaker(p.speakerId);
  }

  get canChooseSpeaker() {
    return typeof (AudioContext.prototype as SinkCtx).setSinkId === 'function';
  }

  /** Opens the microphone and starts sending frames for a call. */
  async startCall(codec: string, onFrame: (f: Uint8Array) => void): Promise<void> {
    await this.unlock();
    this.codec = codec || 'PCMU';
    this.playback!.port.postMessage({ reset: true });
    if (!this.mic) {
      const audio: MediaTrackConstraints = { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 };
      if (this.prefs.micId) audio.deviceId = { exact: this.prefs.micId };
      try {
        this.mic = await navigator.mediaDevices.getUserMedia({ audio });
      } catch (e) {
        if (this.prefs.micId && (e as DOMException).name === 'OverconstrainedError') {
          delete audio.deviceId;
          this.mic = await navigator.mediaDevices.getUserMedia({ audio });
        } else {
          throw e;
        }
      }
    }
    const ctx = this.ctx!;
    this.micSource = ctx.createMediaStreamSource(this.mic);
    this.capture = new AudioWorkletNode(ctx, 'wip-capture', { processorOptions: { codec: this.codec } });
    this.capture.port.onmessage = (e) => {
      if (e.data.frame) onFrame(e.data.frame);
      if (typeof e.data.level === 'number') micLevel.set(e.data.level);
    };
    this.micSource.connect(this.capture);
    this.capture.connect(this.sink!);
  }

  setCodec(codec: string) {
    if (codec && codec !== this.codec) {
      this.codec = codec;
      this.capture?.port.postMessage({ codec });
    }
  }

  play(frame: Uint8Array) {
    this.playback?.port.postMessage({ codec: this.codec, data: frame }, [frame.buffer]);
  }

  stopCall() {
    this.micSource?.disconnect();
    this.capture?.disconnect();
    this.capture?.port.close();
    this.capture = undefined;
    this.micSource = undefined;
    this.mic?.getTracks().forEach((t) => t.stop());
    this.mic = undefined;
    micLevel.set(0);
    this.playback?.port.postMessage({ reset: true });
  }

  // ---- tones -------------------------------------------------------------------------

  private playPattern(freqs: number[], onMs: number, offMs: number, gain: number, repeat: boolean) {
    this.stopTone();
    const ctx = this.ctx;
    if (!ctx || ctx.state !== 'running') return;
    const out = ctx.createGain();
    out.gain.value = 0;
    out.connect(ctx.destination);
    const oscs = freqs.map((f) => {
      const o = ctx.createOscillator();
      o.frequency.value = f;
      o.connect(out);
      o.start();
      return o;
    });
    const period = (onMs + offMs) / 1000;
    const schedule = (from: number, cycles: number) => {
      for (let i = 0; i < cycles; i++) {
        const t = from + i * period;
        out.gain.setTargetAtTime(gain, t, 0.01);
        out.gain.setTargetAtTime(0, t + onMs / 1000, 0.01);
      }
    };
    const start = ctx.currentTime + 0.02;
    schedule(start, repeat ? 60 : 1);
    const stop = () => {
      oscs.forEach((o) => {
        try {
          o.stop();
        } catch {
          /* already stopped */
        }
      });
      out.disconnect();
    };
    if (!repeat) setTimeout(stop, onMs + 100);
    this.tone = { stop };
  }

  stopTone() {
    this.tone?.stop();
    this.tone = undefined;
  }

  /** Incoming call: a soft two-tone trill. */
  ringtone() {
    this.playPattern([440, 480], 1200, 2800, 0.12, true);
  }

  /** Outgoing call, while the far end rings without early media. */
  ringback() {
    this.playPattern([425], 1000, 4000, 0.08, true);
  }

  private static dtmf: Record<string, [number, number]> = {
    '1': [697, 1209], '2': [697, 1336], '3': [697, 1477], A: [697, 1633],
    '4': [770, 1209], '5': [770, 1336], '6': [770, 1477], B: [770, 1633],
    '7': [852, 1209], '8': [852, 1336], '9': [852, 1477], C: [852, 1633],
    '*': [941, 1209], '0': [941, 1336], '#': [941, 1477], D: [941, 1633],
  };

  keyTone(d: string) {
    const f = AudioEngine.dtmf[d];
    if (f) this.playPattern(f, 120, 0, 0.06, false);
  }
}

export const audio = new AudioEngine();
