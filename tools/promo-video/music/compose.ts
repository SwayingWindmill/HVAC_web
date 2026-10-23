// The film's score and sound effects, synthesised from oscillators and noise: no samples, no audio
// files. 120 BPM in D major (B minor for the night), arranged on the film's clock (film/cues.ts) so
// every chapter, cut and click lands on the picture. Writes film/assets/audio/score.wav (48 kHz, 24-bit).
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { CUES } from '../film/cues.ts';

const here = dirname(fileURLToPath(import.meta.url));
const SR = 48000;
const BEAT = 60 / CUES.bpm;
const BAR = BEAT * 4;
const N = Math.ceil((CUES.duration + 0.5) * SR);
const TAU = Math.PI * 2;

// ── Deterministic noise ─────────────────────────────────────────────────────────
let seed = 0x9e3779b9;
const noise = () => {
  seed |= 0; seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 2147483648 - 1;
};
const midi = (note) => 440 * 2 ** ((note - 69) / 12);

// ── Filters ─────────────────────────────────────────────────────────────────────
class Biquad {
  constructor() { this.x1 = this.x2 = this.y1 = this.y2 = 0; this.set('lp', 1000, 0.707); }
  set(type, freq, q = 0.707) {
    const w = (TAU * Math.min(freq, SR * 0.45)) / SR;
    const cos = Math.cos(w);
    const alpha = Math.sin(w) / (2 * q);
    let b0, b1, b2;
    if (type === 'lp') { b0 = (1 - cos) / 2; b1 = 1 - cos; b2 = b0; }
    else if (type === 'hp') { b0 = (1 + cos) / 2; b1 = -(1 + cos); b2 = b0; }
    else { b0 = alpha; b1 = 0; b2 = -alpha; }
    const a0 = 1 + alpha;
    this.b0 = b0 / a0; this.b1 = b1 / a0; this.b2 = b2 / a0; this.a1 = (-2 * cos) / a0; this.a2 = (1 - alpha) / a0;
    return this;
  }
  run(x) {
    const y = this.b0 * x + this.b1 * this.x1 + this.b2 * this.x2 - this.a1 * this.y1 - this.a2 * this.y2;
    this.x2 = this.x1; this.x1 = x; this.y2 = this.y1; this.y1 = y;
    return y;
  }
}

// ── Buses ───────────────────────────────────────────────────────────────────────
const bus = () => [new Float32Array(N), new Float32Array(N)];
const drums = bus(), bass = bus(), pads = bus(), keys = bus(), fx = bus(), sfx = bus(), verb = bus();

/** Mixes a mono or stereo voice into a bus at a time, with gain, pan and a reverb send. */
function place(target, voice, time, gain = 1, pan = 0, send = 0) {
  const start = Math.round(time * SR);
  const [l, r] = Array.isArray(voice) ? voice : [voice, voice];
  const gl = gain * Math.cos(((pan + 1) * Math.PI) / 4) * Math.SQRT2;
  const gr = gain * Math.sin(((pan + 1) * Math.PI) / 4) * Math.SQRT2;
  for (let i = 0; i < l.length; i += 1) {
    const at = start + i;
    if (at < 0 || at >= N) continue;
    target[0][at] += l[i] * gl;
    target[1][at] += r[i] * gr;
    if (send) { verb[0][at] += l[i] * gl * send; verb[1][at] += r[i] * gr * send; }
  }
}

const samples = (seconds) => new Float32Array(Math.round(seconds * SR));
const env = (t, attack, length, release) => (t < attack ? t / attack : t > length - release ? Math.max(0, (length - t) / release) : 1);

// ── Instruments ─────────────────────────────────────────────────────────────────
function kick(weight = 1) {
  const out = samples(0.5);
  let phase = 0;
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    phase += (TAU * (44 + 120 * Math.exp(-t * 32))) / SR;
    const body = Math.sin(phase) * Math.exp(-t * 6.5) * Math.min(1, t / 0.0015);
    const click = t < 0.006 ? noise() * (1 - t / 0.006) * 0.35 : 0;
    out[i] = Math.tanh((body + click) * 1.6 * weight) * 0.9;
  }
  return out;
}

function clap() {
  const out = samples(0.35);
  const band = new Biquad().set('bp', 1400, 0.9);
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    const bursts = [0, 0.011, 0.022].reduce((sum, at) => sum + (t >= at ? Math.exp(-(t - at) * 140) : 0), 0);
    out[i] = band.run(noise()) * (bursts * 0.6 + Math.exp(-t * 16) * 0.5) * 2.2;
  }
  return out;
}

function hat(open = false) {
  const out = samples(open ? 0.3 : 0.06);
  const high = new Biquad().set('hp', 7500, 0.8);
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    out[i] = high.run(noise()) * Math.exp(-t * (open ? 11 : 70)) * 0.8;
  }
  return out;
}

/** The clock: a dry wooden tick, the hook's and the night's metronome. */
function tick(pitch = 1) {
  const out = samples(0.06);
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    out[i] = (Math.sin(TAU * 1900 * pitch * t) * 0.7 + Math.sin(TAU * 3800 * pitch * t) * 0.25) * Math.exp(-t * 110);
  }
  return out;
}

function snare() {
  const out = samples(0.25);
  const band = new Biquad().set('hp', 1800, 0.7);
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    out[i] = (band.run(noise()) * Math.exp(-t * 22) * 0.8 + Math.sin(TAU * 190 * t) * Math.exp(-t * 30) * 0.5);
  }
  return out;
}

function bassNote(note, length) {
  const out = samples(length + 0.05);
  const low = new Biquad();
  const f = midi(note);
  let phase = 0;
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    phase = (phase + f / SR) % 1;
    if (i % 32 === 0) low.set('lp', 140 + 1300 * Math.exp(-t * 11), 1.1);
    const saw = 2 * phase - 1;
    out[i] = (low.run(saw) * 0.55 + Math.sin(TAU * phase) * 0.6) * env(t, 0.004, length + 0.05, 0.05);
  }
  return out;
}

/** A chord of detuned saws through a low-pass, wide in stereo. */
function pad(notes, length, brightness = 1400, attack = 0.35) {
  const l = samples(length + 0.8), r = samples(length + 0.8);
  const voices = notes.flatMap((note) => [-9, 0, 8].map((cents, k) => ({ f: midi(note) * 2 ** (cents / 1200), phase: (k * 0.37 + note * 0.13) % 1, side: k - 1 })));
  const lowL = new Biquad(), lowR = new Biquad();
  const total = length + 0.8;
  for (let i = 0; i < l.length; i += 1) {
    const t = i / SR;
    if (i % 64 === 0) { const cut = brightness * (0.75 + 0.25 * Math.sin(TAU * 0.15 * t)); lowL.set('lp', cut, 0.6); lowR.set('lp', cut * 1.05, 0.6); }
    let sl = 0, sr = 0;
    for (const voice of voices) {
      voice.phase = (voice.phase + voice.f / SR) % 1;
      const saw = 2 * voice.phase - 1;
      sl += saw * (voice.side <= 0 ? 1 : 0.5);
      sr += saw * (voice.side >= 0 ? 1 : 0.5);
    }
    const a = env(t, attack, total, 0.8) / voices.length;
    l[i] = lowL.run(sl) * a * 1.6;
    r[i] = lowR.run(sr) * a * 1.6;
  }
  return [l, r];
}

function pluck(note, length = 0.25, tone = 1) {
  const out = samples(length);
  const low = new Biquad();
  const f = midi(note);
  let phase = 0;
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    phase = (phase + f / SR) % 1;
    if (i % 32 === 0) low.set('lp', 400 + 4200 * tone * Math.exp(-t * 18), 0.9);
    const wave = (2 * phase - 1) * 0.6 + (phase < 0.5 ? 0.4 : -0.4);
    out[i] = low.run(wave) * Math.exp(-t * 7) * Math.min(1, t / 0.002);
  }
  return out;
}

/** A soft bell for the mark: sine partials in bell ratios. */
function bell(note, length = 3) {
  const out = samples(length);
  const f = midi(note);
  const partials = [[1, 1, 1.2], [2.0, 0.45, 2.2], [2.76, 0.3, 3.1], [5.4, 0.12, 5]];
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    let s = 0;
    for (const [ratio, gain, decay] of partials) s += Math.sin(TAU * f * ratio * t) * gain * Math.exp(-t * decay);
    out[i] = s * 0.35 * Math.min(1, t / 0.003);
  }
  return out;
}

function riser(length) {
  const l = samples(length), r = samples(length);
  const bandL = new Biquad(), bandR = new Biquad();
  let phase = 0;
  for (let i = 0; i < l.length; i += 1) {
    const t = i / SR, k = t / length;
    if (i % 32 === 0) { const f = 300 * 20 ** k; bandL.set('bp', f, 1.4); bandR.set('bp', f * 1.07, 1.4); }
    phase = (phase + (110 * 4 ** k) / SR) % 1;
    const a = k ** 2.2;
    l[i] = (bandL.run(noise()) * 1.4 + (2 * phase - 1) * 0.08) * a;
    r[i] = (bandR.run(noise()) * 1.4 + (2 * phase - 1) * 0.08) * a;
  }
  return [l, r];
}

function impact() {
  const l = samples(2.4), r = samples(2.4);
  const lowL = new Biquad().set('lp', 5000, 0.5), lowR = new Biquad().set('lp', 4600, 0.5);
  let phase = 0;
  for (let i = 0; i < l.length; i += 1) {
    const t = i / SR;
    phase += (TAU * (32 + 40 * Math.exp(-t * 7))) / SR;
    const boom = Math.sin(phase) * Math.exp(-t * 2.4) * 0.9;
    const crash = Math.exp(-t * 3.2) * 0.35;
    l[i] = Math.tanh(boom * 1.4) + lowL.run(noise()) * crash;
    r[i] = Math.tanh(boom * 1.4) + lowR.run(noise()) * crash;
  }
  return [l, r];
}

/** A cut's whoosh, its loudest point at `peak` of its length; three shapes so no two neighbours match. */
function whoosh(variant) {
  const length = [0.55, 0.45, 0.65][variant % 3];
  const peak = 0.72;
  const l = samples(length), r = samples(length);
  const bandL = new Biquad(), bandR = new Biquad();
  for (let i = 0; i < l.length; i += 1) {
    const t = i / SR, k = t / length;
    if (i % 32 === 0) {
      const f = [500, 900, 350][variant % 3] * (k < peak ? 1 + 5 * (k / peak) ** 2 : 6 - 3 * (k - peak) / (1 - peak));
      bandL.set('bp', f, 0.9); bandR.set('bp', f * 1.12, 0.9);
    }
    const a = k < peak ? (k / peak) ** 2.5 : Math.exp(-(k - peak) * 14);
    const pan = (k - 0.5) * (variant % 2 ? 1 : -1);
    l[i] = bandL.run(noise()) * a * (1 - pan * 0.6);
    r[i] = bandR.run(noise()) * a * (1 + pan * 0.6);
  }
  return { voice: [l, r], lead: length * peak };
}

function click() {
  const out = samples(0.03);
  const high = new Biquad().set('hp', 2500, 0.7);
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    out[i] = (high.run(noise()) * Math.exp(-t * 600) + Math.sin(TAU * 2600 * t) * Math.exp(-t * 300) * 0.5) * 0.9;
  }
  return out;
}

function pop(note = 81) {
  const out = samples(0.18);
  for (let i = 0; i < out.length; i += 1) {
    const t = i / SR;
    const f = midi(note) * (1 + 0.5 * Math.exp(-t * 60));
    out[i] = Math.sin(TAU * f * t) * Math.exp(-t * 26) * Math.min(1, t / 0.002) * 0.8;
  }
  return out;
}

// ── Harmony ─────────────────────────────────────────────────────────────────────
const CHORDS = {
  D: { pad: [50, 57, 62, 66], root: 38, arp: [62, 66, 69, 74] },
  A: { pad: [49, 57, 61, 64], root: 33, arp: [61, 64, 69, 73] },
  Bm: { pad: [47, 54, 59, 62], root: 35, arp: [59, 62, 66, 71] },
  G: { pad: [47, 55, 59, 62], root: 31, arp: [59, 62, 67, 71] },
  Em: { pad: [52, 55, 59, 64], root: 40, arp: [59, 64, 67, 71] },
  Fs: { pad: [49, 54, 58, 61], root: 30, arp: [58, 61, 66, 70] },
  Bm9: { pad: [47, 54, 61, 62], root: 35, arp: [59, 61, 66, 71] },
  Asus: { pad: [50, 57, 62, 64], root: 33, arp: [62, 64, 69, 74] },
};
const LOOP = ['D', 'A', 'Bm', 'G'];
/** The chord of each bar (1-based), by section. */
/** The bar the mark lands on opens the ending; the bar before it rises into it. */
const END_BAR = Math.round(CUES.mark / (BEAT * 4)) + 1;
function chordOf(barIndex) {
  if (barIndex <= 2) return 'Bm9';
  if (barIndex === 3) return 'G';
  if (barIndex === 4) return 'Asus';
  if (barIndex >= 16 && barIndex <= 20) return ['Bm', 'G', 'Em', 'Fs', 'G'][barIndex - 16];
  // The bar before the mark sits on A and drives into it; the mark resolves home to D.
  if (barIndex >= END_BAR - 1) return barIndex === END_BAR - 1 ? 'A' : 'D';
  return LOOP[(barIndex - 5) % 4];
}
const barStart = (barIndex) => (barIndex - 1) * BAR;
const BARS = Math.ceil(CUES.duration / BAR);
const kicks = [];

for (let b = 1; b <= BARS; b += 1) {
  const t0 = barStart(b);
  const chord = CHORDS[chordOf(b)];
  const section = b <= 3 ? 'hook' : b === 4 ? 'build' : b <= 15 ? 'groove' : b <= 19 ? 'night' : b === 20 ? 'rise' : b <= END_BAR - 1 ? 'finale' : 'end';
  const lift = b >= 10 && b <= 15;

  // Pads: every bar, quiet in the hook, open in the finale, warm at the end.
  const brightness = { hook: 700, build: 900 + 0, groove: 1500, night: 800, rise: 1100, finale: 2100, end: 1700 }[section];
  const padGain = { hook: 0.16, build: 0.2, groove: 0.2, night: 0.22, rise: 0.22, finale: 0.22, end: 0.26 }[section];
  if (section !== 'end' || b === END_BAR) place(pads, pad(chord.pad, section === 'end' ? BAR * 4 : BAR, brightness, section === 'hook' ? 0.9 : 0.25), t0, padGain, 0, 0.35);

  // The clock ticks through the hook and the night.
  if (section === 'hook' || section === 'night') for (let k = 0; k < 4; k += 1) place(drums, tick(k === 0 ? 1.12 : 1), t0 + k * BEAT, section === 'hook' ? 0.22 : 0.16, 0.35, 0.15);

  // Drums
  if (section === 'groove' || section === 'finale' || section === 'build' || section === 'rise') {
    for (let k = 0; k < 4; k += 1) {
      const at = t0 + k * BEAT;
      if (section === 'build' || section === 'rise') {
        place(drums, kick(0.8), at, 0.5); kicks.push(at);
        for (let s = 0; s < 4; s += 1) place(drums, snare(), at + s * BEAT / 4, 0.06 + 0.11 * ((k * 4 + s) / 16), 0, 0.2);
        continue;
      }
      place(drums, kick(), at, 0.78); kicks.push(at);
      if (k % 2 === 1) place(drums, clap(), at, 0.32, 0, 0.25);
      place(drums, hat(section === 'finale' && k === 3), at + BEAT / 2, 0.16, 0.25);
      if (lift || section === 'finale') { place(drums, hat(), at + BEAT / 4, 0.07, -0.25); place(drums, hat(), at + (3 * BEAT) / 4, 0.07, -0.25); }
    }
  }
  if (b === END_BAR - 1) {
    // Into the mark the groove keeps going and a snare roll climbs over it, sixteenths to thirty-seconds.
    for (let s = 0; s < 24; s += 1) {
      const k = s / 23;
      place(drums, snare(), t0 + (s < 8 ? s * BEAT / 4 : 2 * BEAT + (s - 8) * BEAT / 8), 0.07 + 0.22 * k * k, 0, 0.25);
    }
  }
  if (section === 'night') {
    // A heartbeat instead of the kick.
    for (const at of [t0, t0 + BEAT * 0.45, t0 + BEAT * 2, t0 + BEAT * 2.45]) { place(drums, kick(0.55), at, 0.42); kicks.push(at); }
  }

  // Bass: eighths in the groove, held under the night and the end.
  if (section === 'groove' || section === 'finale' || section === 'build' || section === 'rise') {
    for (let e = 0; e < 8; e += 1) {
      const note = chord.root + (e % 4 === 3 ? 12 : 0);
      place(bass, bassNote(note, BEAT / 2 - 0.02), t0 + (e * BEAT) / 2, section === 'build' ? 0.3 : 0.42);
    }
  } else if (section === 'night' || section === 'hook' || section === 'end') {
    if (section !== 'end' || b === END_BAR) place(bass, bassNote(chord.root, section === 'end' ? BAR * 3.5 : BAR - 0.05), t0, section === 'hook' ? 0.22 : 0.34);
  }

  // Keys: sixteenth arpeggios, an octave up while the film thinks and saves.
  if (section === 'groove' || section === 'finale') {
    for (let s = 0; s < 16; s += 1) {
      const order = [0, 1, 2, 3, 2, 1, 2, 3][s % 8];
      const note = chord.arp[order] + (lift || section === 'finale' ? 12 : 0);
      place(keys, pluck(note, 0.22, section === 'finale' ? 1.2 : 0.9), t0 + (s * BEAT) / 4, s % 4 === 0 ? 0.17 : 0.11, s % 2 ? 0.45 : -0.45, 0.25);
    }
  }
  if (section === 'night' && b >= 17) {
    // The alarm motif: two notes, a semitone apart, asking.
    for (const [k, note] of [[0, 78], [1, 79], [2, 78], [3, 79]]) place(keys, pluck(note, 0.3, 0.6), t0 + k * BEAT, 0.08, 0.3, 0.4);
  }
}

// Risers into the drop, the night's turn and the mark.
for (const [end, length] of [[CUES.drop, 2], [CUES.save, 2], [CUES.mark, 3.6]]) place(fx, riser(length), end - length, 0.32);
for (const at of CUES.impacts) { place(fx, impact(), at, 0.6, 0, 0.5); place(drums, kick(1.2), at, 0.7); }
// The mark: a bell arpeggio on D over the last chord.
[74, 78, 81, 86].forEach((note, k) => place(keys, bell(note, 4), CUES.mark + 0.18 + k * 0.12, 0.2, (k - 1.5) * 0.3, 0.6));
// Chapter words hit with a snare and a crash of noise.
for (const at of [CUES.see, CUES.think, CUES.night, CUES.save]) place(drums, snare(), at, 0.32, 0, 0.45);

// Sounds on picture: each lands one frame after what causes it.
const frame = 1 / 30;
CUES.whooshes.forEach((at, k) => { const { voice, lead } = whoosh(k); place(sfx, voice, at - lead + frame, 0.22, 0, 0.2); });
CUES.clicks.forEach((at) => place(sfx, click(), at + frame, 0.5, 0.2));
CUES.pops.forEach((at, k) => place(sfx, pop([81, 83, 86, 88, 90][k % 5]), at + frame, 0.32, 0, 0.2));

// ── Sidechain: pads, bass and keys duck under every kick ───────────────────────
const duck = new Float32Array(N).fill(1);
for (const at of kicks) {
  const start = Math.round(at * SR);
  for (let i = 0; i < SR * 0.3 && start + i < N; i += 1) duck[start + i] = Math.min(duck[start + i], 1 - 0.55 * Math.exp(-(i / SR) / 0.09));
}
for (const target of [pads, bass, keys]) for (const ch of target) for (let i = 0; i < N; i += 1) ch[i] *= duck[i];

// ── Ping-pong delay on the keys (dotted eighth) ─────────────────────────────────
{
  const d = Math.round(BEAT * 0.75 * SR);
  for (let i = d; i < N; i += 1) {
    keys[0][i] += keys[1][i - d] * 0.32;
    keys[1][i] += keys[0][i - d] * 0.32;
  }
}

// ── Reverb (Freeverb: 8 combs, 4 allpasses per side) on the send ───────────────
function freeverb(input, spread) {
  const scale = SR / 44100;
  const combs = [1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617].map((n) => ({ buf: new Float32Array(Math.round((n + spread) * scale)), i: 0, store: 0 }));
  const alls = [556, 441, 341, 225].map((n) => ({ buf: new Float32Array(Math.round((n + spread) * scale)), i: 0 }));
  const out = new Float32Array(N);
  const feedback = 0.86, damp = 0.25;
  for (let n = 0; n < N; n += 1) {
    const x = input[n] * 0.015;
    let sum = 0;
    for (const c of combs) {
      const y = c.buf[c.i];
      c.store = y * (1 - damp) + c.store * damp;
      c.buf[c.i] = x + c.store * feedback;
      c.i = (c.i + 1) % c.buf.length;
      sum += y;
    }
    for (const a of alls) {
      const y = a.buf[a.i];
      a.buf[a.i] = sum + y * 0.5;
      a.i = (a.i + 1) % a.buf.length;
      sum = y - sum;
    }
    out[n] = sum;
  }
  return out;
}
const wet = [freeverb(verb[0], 0), freeverb(verb[1], 23)];

// The low end belongs to the kick and the bass: pads and keys give it up.
for (const [stem, cut] of [[pads, 190], [keys, 240]]) {
  for (const ch of stem) {
    const high = new Biquad().set('hp', cut, 0.7);
    for (let i = 0; i < N; i += 1) ch[i] = high.run(ch[i]);
  }
}

// ── Mix and master ──────────────────────────────────────────────────────────────
const stems = [[drums, 1], [bass, 0.95], [pads, 0.9], [keys, 0.85], [fx, 0.9], [sfx, 1], [wet, 1.1]];
const master = bus();
const rumble = [new Biquad().set('hp', 28, 0.7), new Biquad().set('hp', 28, 0.7)];
for (let ch = 0; ch < 2; ch += 1) {
  for (let i = 0; i < N; i += 1) {
    let s = 0;
    for (const [stem, gain] of stems) s += stem[ch][i] * gain;
    master[ch][i] = rumble[ch].run(s);
  }
}
// Fade the tail into the end of the film.
const fadeFrom = Math.round((CUES.duration - 1.6) * SR);
for (let i = fadeFrom; i < N; i += 1) { const k = Math.max(0, 1 - (i - fadeFrom) / (1.6 * SR)); master[0][i] *= k; master[1][i] *= k; }
// Soft clip, then a true-peak-ish ceiling at −1 dBFS.
let peak = 0;
for (const ch of master) for (let i = 0; i < N; i += 1) { ch[i] = Math.tanh(ch[i] * 1.15); peak = Math.max(peak, Math.abs(ch[i])); }
const ceiling = 10 ** (-1 / 20) / peak;
let energy = 0;
for (const ch of master) for (let i = 0; i < N; i += 1) { ch[i] *= ceiling; energy += ch[i] * ch[i]; }
const rms = 10 * Math.log10(energy / (2 * N));

// ── Write 24-bit WAV ────────────────────────────────────────────────────────────
const length = Math.round(CUES.duration * SR);
const data = Buffer.alloc(length * 2 * 3);
for (let i = 0; i < length; i += 1) {
  for (let ch = 0; ch < 2; ch += 1) {
    const v = Math.max(-1, Math.min(1, master[ch][i]));
    data.writeIntLE(Math.round(v * 8388607), (i * 2 + ch) * 3, 3);
  }
}
const header = Buffer.alloc(44);
header.write('RIFF', 0); header.writeUInt32LE(36 + data.length, 4); header.write('WAVE', 8);
header.write('fmt ', 12); header.writeUInt32LE(16, 16); header.writeUInt16LE(1, 20); header.writeUInt16LE(2, 22);
header.writeUInt32LE(SR, 24); header.writeUInt32LE(SR * 6, 28); header.writeUInt16LE(6, 32); header.writeUInt16LE(24, 34);
header.write('data', 36); header.writeUInt32LE(data.length, 40);
const out = join(here, '..', 'film', 'assets', 'audio');
mkdirSync(out, { recursive: true });
writeFileSync(join(out, 'score.wav'), Buffer.concat([header, data]));
console.log(`score.wav: ${CUES.duration}s, ${BARS} bars at ${CUES.bpm} BPM, RMS ${rms.toFixed(1)} dBFS`);
