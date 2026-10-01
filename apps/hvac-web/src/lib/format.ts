export function formatDate(
  date: Date | string | number | undefined,
  opts: Intl.DateTimeFormatOptions = {},
) {
  if (!date) return '';

  try {
    return new Intl.DateTimeFormat('zh-CN', {
      month: opts.month ?? 'numeric',
      day: opts.day ?? 'numeric',
      year: opts.year ?? 'numeric',
      ...opts,
    }).format(new Date(date));
  } catch {
    return '';
  }
}

export type NumberFormat = 'number' | 'compact' | 'currency' | 'percent';

export interface FormatNumberOptions {
  format?: NumberFormat;
  currency?: string;
  locale?: string;
  maximumFractionDigits?: number;
  compact?: boolean;
}

const formatterCache = new Map<string, Intl.NumberFormat>();

function getFormatter(locale: string, options: Intl.NumberFormatOptions) {
  const key = locale + JSON.stringify(options);
  let formatter = formatterCache.get(key);
  if (!formatter) {
    formatter = new Intl.NumberFormat(locale, options);
    formatterCache.set(key, formatter);
  }
  return formatter;
}

export function formatNumber(
  value: number,
  {
    format = 'number',
    currency = 'CNY',
    locale = 'zh-CN',
    maximumFractionDigits,
    compact = false,
  }: FormatNumberOptions = {},
): string {
  if (!Number.isFinite(value)) return '—';

  const compactOptions: Intl.NumberFormatOptions = compact
    ? maximumFractionDigits === undefined
      ? { notation: 'compact', maximumSignificantDigits: 3 }
      : { notation: 'compact', maximumFractionDigits }
    : {};

  switch (format) {
    case 'compact':
      return getFormatter(locale, {
        notation: 'compact',
        maximumFractionDigits: maximumFractionDigits ?? 1,
      }).format(value);
    case 'currency':
      return getFormatter(locale, {
        style: 'currency',
        currency,
        maximumFractionDigits: maximumFractionDigits ?? 2,
        ...compactOptions,
      }).format(value);
    case 'percent':
      return getFormatter(locale, {
        style: 'percent',
        maximumFractionDigits: maximumFractionDigits ?? 1,
      }).format(value);
    default:
      return getFormatter(locale, {
        maximumFractionDigits: maximumFractionDigits ?? 0,
        ...compactOptions,
      }).format(value);
  }
}

export function formatDelta(delta: number, locale = 'zh-CN'): string {
  if (!Number.isFinite(delta)) return '—';
  return getFormatter(locale, {
    style: 'percent',
    signDisplay: 'exceptZero',
    maximumFractionDigits: 1,
    minimumFractionDigits: 1,
  }).format(delta);
}

export function computeDelta(current: number, previous: number): number {
  if (!previous) return 0;
  return (current - previous) / Math.abs(previous);
}

