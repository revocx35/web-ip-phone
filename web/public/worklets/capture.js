// Microphone -> 8 kHz G.711 frames (160 bytes = 20 ms), posted to the main thread.
// Runs on the audio rendering thread; must stay allocation-free in process().

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

function linearToUlaw(s) {
  const BIAS = 0x84, CLIP = 32635;
  let sign = 0;
  if (s < 0) { s = -s; sign = 0x80; }
  if (s > CLIP) s = CLIP;
  s += BIAS;
  let exp = 7;
  for (let mask = 0x4000; (s & mask) === 0 && exp > 0; mask >>= 1) exp--;
  const mant = (s >> (exp + 3)) & 0x0f;
  return ~(sign | (exp << 4) | mant) & 0xff;
}

function linearToAlaw(s) {
  let mask;
  if (s >= 0) mask = 0xd5; else { mask = 0x55; s = -s - 1; }
  if (s > 32767) s = 32767;
  let seg = 0;
  const ends = [256, 512, 1024, 2048, 4096, 8192, 16384, 32768];
  while (seg < 8 && s >= ends[seg]) seg++;
  let aval = seg < 2 ? (s >> 4) & 0x0f : (s >> (seg + 3)) & 0x0f;
  aval |= seg << 4;
  return (aval ^ mask) & 0xff;
}

// Lookup tables over the 16-bit range (64 KiB each) keep process() cheap.
const ULAW = new Uint8Array(65536);
const ALAW = new Uint8Array(65536);
for (let i = 0; i < 65536; i++) {
  const s = i - 32768;
  ULAW[i] = linearToUlaw(s);
  ALAW[i] = linearToAlaw(s);
}

class CaptureProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super();
    this.table = options.processorOptions?.codec === 'PCMA' ? ALAW : ULAW;
    this.step = sampleRate / 8000;
    this.pos = 0;
    this.prev = 0;
    // 4th-order Butterworth low-pass (two biquads) below the 4 kHz Nyquist of the output.
    this.f1 = lowpass(sampleRate, 3500, 0.5412);
    this.f2 = lowpass(sampleRate, 3500, 1.3066);
    this.frame = new Uint8Array(160);
    this.n = 0;
    this.sq = 0;
    this.frames = 0;
    this.port.onmessage = (e) => {
      if (e.data.codec) this.table = e.data.codec === 'PCMA' ? ALAW : ULAW;
    };
  }

  process(inputs) {
    const input = inputs[0];
    if (!input || input.length === 0) return true;
    const ch = input[0];
    const ch2 = input.length > 1 ? input[1] : null;
    for (let i = 0; i < ch.length; i++) {
      const raw = ch2 ? (ch[i] + ch2[i]) * 0.5 : ch[i];
      const x = run(this.f2, run(this.f1, raw));
      this.pos += 1;
      if (this.pos >= this.step) {
        this.pos -= this.step;
        const y = x - (x - this.prev) * this.pos;
        let v = Math.round(y * 32767);
        if (v > 32767) v = 32767; else if (v < -32768) v = -32768;
        this.sq += v * v;
        this.frame[this.n++] = this.table[v + 32768];
        if (this.n === 160) {
          const out = this.frame.slice();
          this.n = 0;
          this.frames++;
          let level;
          if (this.frames % 5 === 0) {
            level = Math.sqrt(this.sq / 800) / 32768;
            this.sq = 0;
          }
          this.port.postMessage({ frame: out, level }, [out.buffer]);
        }
      }
      this.prev = x;
    }
    return true;
  }
}

registerProcessor('wip-capture', CaptureProcessor);
