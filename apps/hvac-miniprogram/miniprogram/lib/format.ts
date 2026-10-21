// Numbers as operators read them on the web pages. Written out by hand: the phone's locale and Intl
// support differ between WeChat clients.

/** A number with thousands separators and a fixed number of decimals; a missing value is a dash. */
export function grouped(value: number | null, decimals = 0): string {
  if (value === null || !Number.isFinite(value)) return '—';
  const [whole, fraction] = Math.abs(value).toFixed(decimals).split('.');
  const withCommas = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return `${value < 0 ? '-' : ''}${withCommas}${fraction === undefined ? '' : `.${fraction}`}`;
}

export type ChangeSense = 'less-is-better' | 'more-is-better' | 'neutral';

export interface ChangeBadge {
  readonly text: string;
  readonly tone: 'good' | 'bad' | 'neutral';
}

/** A change as the design system draws it: coloured by whether it is good or bad, not by its direction. */
export function changeBadge(change: number | null, sense: ChangeSense): ChangeBadge | null {
  if (change === null) return null;
  const percent = Math.round(change * 1000) / 10;
  if (percent === 0) return { text: '持平', tone: 'neutral' };
  const up = percent > 0;
  const tone = sense === 'neutral' ? 'neutral' : (up === (sense === 'more-is-better') ? 'good' : 'bad');
  return { text: `${up ? '▲' : '▼'} ${Math.abs(percent).toFixed(1)}%`, tone };
}
