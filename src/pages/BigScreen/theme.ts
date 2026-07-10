export const BG = '#080c18';
export const PANEL_BG = '#0d1424';
export const PANEL_BD = '#1a2744';
export const TEXT = '#eaf0fb';
export const DIM = '#7a8baa';

export const ACCENT = '#0FB5AE';
export const ACCENT_DK = '#0E9C96';

export const AMBER = '#f5a623';
export const RED = '#ef4444';
export const GREEN = '#22c55e';
export const RADIUS = 12;

export const hexA = (hex: string, a: number) => {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${a})`;
};
