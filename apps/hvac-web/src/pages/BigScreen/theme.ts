export const BG = '#070b14';
export const PANEL_BG = '#0a111d';
export const PANEL_BD = '#1b2a40';
export const TEXT = '#f2f6fc';
export const DIM = '#8290a8';

export const ACCENT = '#27c2b7';
export const ACCENT_DK = '#159f97';

export const AMBER = '#f2ad45';
export const RED = '#ef5b62';
export const GREEN = '#36c98f';
export const RADIUS = 12;

export const hexA = (hex: string, a: number) => {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${a})`;
};
