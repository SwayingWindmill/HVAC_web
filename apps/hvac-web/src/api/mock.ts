import type { LatestMap, TimeseriesMap, DeviceSnapshot, Range, TelemetryPoint } from './types';

// Devices we simulate (mirrors mockTree leaf nodes in building b1).
export const MOCK_DEVICES = ['b1-z1-u1', 'b1-z1-u2', 'b1-z1-p3', 'b1-z2-ahu1', 'b1-z2-ahu2', 'b1-z3-ahu7'];

interface MetricSpec {
  base: number;
  jitter: number;
  min: number;
  max: number;
}
const SPECS: Record<string, MetricSpec> = {
  supplyTemp: { base: 7, jitter: 0.3, min: 5, max: 12 },
  returnTemp: { base: 12, jitter: 0.3, min: 9, max: 16 },
  power: { base: 180, jitter: 12, min: 50, max: 400 },
  cop: { base: 4.8, jitter: 0.15, min: 3.5, max: 6.5 },
  load: { base: 63, jitter: 4, min: 20, max: 100 },
  flow: { base: 320, jitter: 10, min: 100, max: 500 },
};
export const MOCK_KEYS = Object.keys(SPECS);

const state = new Map<string, Map<string, number>>();
function ensure(deviceId: string): Map<string, number> {
  let m = state.get(deviceId);
  if (!m) {
    m = new Map();
    Object.entries(SPECS).forEach(([k, s]) => m!.set(k, s.base));
    state.set(deviceId, m);
  }
  return m;
}
MOCK_DEVICES.forEach(ensure);

function step(deviceId: string, key: string): number {
  const m = ensure(deviceId);
  const cur = m.get(key) ?? SPECS[key].base;
  const s = SPECS[key];
  const next = cur + (Math.random() - 0.5) * s.jitter * 2;
  const clamped = Math.max(s.min, Math.min(s.max, next));
  m.set(key, clamped);
  return clamped;
}

function latestSnapshot(deviceId: string, keys: string[]): LatestMap {
  const m = ensure(deviceId);
  const ts = Date.now();
  const out: LatestMap = {};
  keys.forEach((k) => {
    out[k] = { ts, value: Math.round((m.get(k) ?? SPECS[k].base) * 100) / 100 };
  });
  return out;
}

export function mockGetLatest(deviceId: string, keys: string[]): Promise<LatestMap> {
  return Promise.resolve(latestSnapshot(deviceId, keys));
}

function rangePoints(range: Range, key: string): TelemetryPoint[] {
  const n =
    range === '1h' ? 60 : range === '6h' ? 72 : range === '24h' ? 96 : range === 'day' ? 24 : range === 'week' ? 7 : 30;
  const stepMs = range === 'day' ? 3_600_000 : range === 'week' || range === 'month' ? 86_400_000 : 60_000;
  let v = SPECS[key].base;
  const now = Date.now();
  const pts: TelemetryPoint[] = [];
  for (let i = n - 1; i >= 0; i--) {
    v = Math.max(SPECS[key].min, Math.min(SPECS[key].max, v + (Math.random() - 0.5) * SPECS[key].jitter * 4));
    pts.push({ ts: now - i * stepMs, value: Math.round(v * 100) / 100 });
  }
  return pts;
}

export function mockGetTimeseries(_deviceId: string, keys: string[], range: Range): Promise<TimeseriesMap> {
  const out: TimeseriesMap = {};
  keys.forEach((k) => {
    out[k] = rangePoints(range, k);
  });
  return Promise.resolve(out);
}

export function mockGetBatch(deviceIds: string[], keys: string[]): Promise<DeviceSnapshot[]> {
  return Promise.resolve(deviceIds.map((d) => ({ deviceId: d, latest: latestSnapshot(d, keys) })));
}

// ---- mock realtime push simulator (drives MockTransport) ----
type PushFn = (deviceId: string, key: string, value: number, ts: number) => void;
const pushHandlers = new Set<PushFn>();
let timer: ReturnType<typeof setInterval> | null = null;

export function onMockPush(h: PushFn): () => void {
  pushHandlers.add(h);
  return () => {
    pushHandlers.delete(h);
  };
}

// ---- derived-only KPI fields the backend does NOT yet aggregate (#4 gap) ----
// energyToday / savingToday / trends are daily rollups with no telemetry source yet,
// so they live here as a stable baseline the Dashboard reads through the same layer.
export const MOCK_KPI = {
  energyToday: 4820,
  energyYesterday: 5210,
  savingToday: 412,
  savingRate: 15.8,
  savingTarget: 15,
  costSavingToday: 2860,
  costSavingMonth: 84210,
  wetBulb: 24.4,
  dataLatency: 2,
  controlMode: '自动群控',
  trends: { energy: -3.2, power: 1.1, cop: 2.4, load: -1.8, saving: 6.5 },
};

export function startMockPush(): () => void {
  if (timer) return () => {};
  timer = setInterval(() => {
    const ts = Date.now();
    MOCK_DEVICES.forEach((d) => {
      Object.keys(SPECS).forEach((k) => {
        const v = Math.round(step(d, k) * 100) / 100;
        pushHandlers.forEach((h) => h(d, k, v, ts));
      });
    });
  }, 1500);
  return () => {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  };
}
