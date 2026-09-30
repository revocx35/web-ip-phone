// 8 kHz G.711 frames from the server -> speakers, with an adaptive jitter buffer.
// Audio arrives over TCP (WebSocket): no loss, but bursts after network stalls. The buffer
// grows after an underrun and trims excess latency after a burst.

function lowpass(fs, f0, q) {
  const w = (2 * Math.PI * f0) / fs;
  const alpha = Math.sin(w) / (2 * q);
  const cos = Math.cos(w);
  const a0 = 1 + alpha;
  return {
    b0: (1 - cos) / 2 / a0, b1: (1 - cos) / a0, b2: (1 - cos) / 2 / a0,
    a1: (-2 * cos) / a0, a2: (1 - alpha) / a0,
    x1: 0, x2: 0, y1: 0, y2: 0,
  };
}

function run(f, x) {
  const y = f.b0 * x + f.b1 * f.x1 + f.b2 * f.x2 - f.a1 * f.y1 - f.a2 * f.y2;
  f.x2 = f.x1; f.x1 = x; f.y2 = f.y1; f.y1 = y;
  return y;
}

const ULAW = new Float32Array(256);
const ALAW = new Float32Array(256);
for (let i = 0; i < 256; i++) {
  let u = ~i & 0xff;
  let t = ((u & 0x0f) << 3) + 0x84;
  t <<= (u & 0x70) >> 4;
  ULAW[i] = ((u & 0x80) ? 0x84 - t : t - 0x84) / 32768;

  let a = i ^ 0x55;
  let v = (a & 0x0f) << 4;
  const seg = (a & 0x70) >> 4;
  if (seg === 0) v += 8;
  else if (seg === 1) v += 0x108;
  else { v += 0x108; v <<= seg - 1; }
  ALAW[i] = ((a & 0x80) ? v : -v) / 32768;
}

const RATE = 8000;
const CAP = RATE * 4; // 4 s ring buffer
const MIN_TARGET = 320; // 40 ms
const MAX_TARGET = 1600; // 200 ms

class PlaybackProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.buf = new Float32Array(CAP);
    this.r = 0;
    this.w = 0;
    this.count = 0;
    this.target = 480; // 60 ms to start
    this.playing = false;
    this.stable = 0;
    this.step = RATE / sampleRate;
    this.phase = 0;
    this.cur = 0;
    this.next = 0;
    this.f1 = lowpass(sampleRate, 3600, 0.5412);
    this.f2 = lowpass(sampleRate, 3600, 1.3066);
    this.volume = 1;
    this.underruns = 0;
    this.tick = 0;
    this.port.onmessage = (e) => {
      const d = e.data;
      if (d.reset) {
        this.r = this.w = this.count = 0;
        this.playing = false;
        this.target = 480;
        return;
      }
      if (typeof d.volume === 'number') this.volume = d.volume;
      if (d.data) this.push(d.data, d.codec === 'PCMA' ? ALAW : ULAW);
    };
  }

  push(bytes, table) {
    for (let i = 0; i < bytes.length; i++) {
      if (this.count === CAP) {
        this.r = (this.r + 1) % CAP;
        this.count--;
      }
      this.buf[this.w] = table[bytes[i]];
      this.w = (this.w + 1) % CAP;
      this.count++;
    }
    // After a burst (network stall), drop the backlog instead of lagging behind.
    if (this.count > this.target + 800) {
      const drop = this.count - this.target;
      this.r = (this.r + drop) % CAP;
      this.count -= drop;
    }
  }

  process(_inputs, outputs) {
    const out = outputs[0];
    const ch = out[0];
    if (!this.playing) {
      if (this.count >= this.target) {
        this.playing = true;
      } else {
        for (let c = 0; c < out.length; c++) out[c].fill(0);
        return true;
      }
    }
    for (let i = 0; i < ch.length; i++) {
      this.phase += this.step;
      while (this.phase >= 1) {
        this.phase -= 1;
        this.cur = this.next;
        if (this.count > 0) {
          this.next = this.buf[this.r];
          this.r = (this.r + 1) % CAP;
          this.count--;
          this.stable++;
        } else {
          this.next = 0;
          if (this.playing) {
            this.playing = false;
            this.underruns++;
            this.target = Math.min(MAX_TARGET, this.target + 160);
            this.stable = 0;
          }
        }
      }
      const y = this.cur + (this.next - this.cur) * this.phase;
      ch[i] = run(this.f2, run(this.f1, y)) * this.volume;
    }
    // Slowly give back latency while the network is stable (every 10 s: -10 ms).
    if (this.stable > RATE * 10) {
      this.stable = 0;
      this.target = Math.max(MIN_TARGET, this.target - 80);
    }
    for (let c = 1; c < out.length; c++) out[c].set(ch);
    if (++this.tick % 100 === 0) {
      this.port.postMessage({ buffered: this.count / RATE, target: this.target / RATE, underruns: this.underruns });
    }
    return true;
  }
}

registerProcessor('wip-playback', PlaybackProcessor);
