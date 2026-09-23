// Shared visual tokens derived from DESIGN.md and the approved HVAC 01–08 references.
// Tailwind and semantic CSS variables consume this palette across product surfaces.

export const BRAND = {
  primary: '#1677FF',
  primaryStrong: '#0958D9',
  primarySoft: '#E6F4FF',
  primaryBorder: '#91CAFF',
  navy: '#102A72',
} as const;

export const TEXT = {
  primary: '#101828',
  secondary: '#475467',
  tertiary: '#667085',
  faint: '#98A2B3',
} as const;

export const SURFACE = {
  base: '#FFFFFF',
  subtle: '#F8FAFC',
  muted: '#F5F7FA',
  border: '#E3E8EF',
  borderStrong: '#D6DEE9',
} as const;

// Semantic status palette. Status meaning is independent from the brand accent.
export const STATUS = {
  ok: '#12B76A',
  warn: '#F79009',
  err: '#F04438',
  info: '#2F80ED',
} as const;

export const RADIUS = {
  xs: 4,
  panel: 6,
  control: 6,
  md: 8,
  drawer: 12,
  pill: 999,
} as const;

export const SPACING = {
  x1: 4,
  x2: 8,
  compact: 10,
  x3: 12,
  x4: 16,
  x5: 20,
  x6: 24,
} as const;

export const CONTROL_HEIGHT = {
  small: 24,
  default: 32,
  large: 40,
} as const;

// Maps backend severity to the shared semantic status vocabulary.
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

export const COP_GOOD = 4.5;
export const LOAD_COMFORT = [40, 85] as const;
