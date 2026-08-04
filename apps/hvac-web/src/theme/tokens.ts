// Design language from prototype variant D (settled in wayfinder #6).
// Single brand accent (teal) locked + semantic status colors separated (no color bleed).
// Mirrors what /design-taste-frontend hygiene required: one accent, contrast-safe tokens.

export const BRAND = {
  teal: '#0FB5AE', // single brand accent
  tealStrong: '#0E9C96',
  deepTeal: '#0B4A4C',
  tealSoft: '#E6FAF9',
} as const;

// Semantic status palette — used for alarms / gauges / KPI deltas.
export const STATUS = {
  ok: '#16A34A',
  warn: '#F59E0B',
  err: '#DC2626',
  info: '#2563EB',
} as const;

// Maps backend severity (from #4 contract) -> antd tone + token.
export type Severity = 'critical' | 'major' | 'minor' | 'info';
export const SEVERITY_TONE: Record<Severity, 'error' | 'warning' | 'info' | 'success'> = {
  critical: 'error',
  major: 'warning',
  minor: 'info',
  info: 'success',
};
export const SEVERITY_COLOR: Record<Severity, string> = {
  critical: STATUS.err,
  major: STATUS.warn,
  minor: STATUS.info,
  info: STATUS.ok,
};
export const SEVERITY_LABEL: Record<Severity, string> = {
  critical: '紧急',
  major: '重要',
  minor: '次要',
  info: '提示',
};

export const COP_GOOD = 4.5; // COP 健康阈值
export const LOAD_COMFORT = [40, 85] as const; // 综合负荷率舒适区间 %
