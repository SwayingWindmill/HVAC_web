// A finding's hourly chart as a picture: an SVG in a data URI, shown with <image>. An image scrolls
// with the page like any other element; a canvas drawn on the phone can be left floating over the
// page and the tab bar while the answer scrolls.

/** A point across (0–1) and up (0–1) the plot; null where the read had no value. */
export type LinePoint = { readonly x: number; readonly y: number } | null;

// The design system's chart colours (chart-1 … chart-4).
const COLORS = ['#3f5bd9', '#0e9384', '#8b5cf6', '#d6409f'];
const GRID = '#e7e7ea';
const WIDTH = 300;
const HEIGHT = 120;
// Room above and below so the line's width is not cut at the top and bottom values.
const INSET = 4;

const tenth = (value: number) => Math.round(value * 10) / 10;
const down = (y: number) => tenth(INSET + (HEIGHT - INSET * 2) * (1 - y));
const at = (point: { x: number; y: number }) => `${tenth(point.x * WIDTH)} ${down(point.y)}`;

/** Each unbroken run of readings as one stroke; a reading alone between gaps as a dot. */
function pathOf(points: readonly LinePoint[]): string {
  const runs: { x: number; y: number }[][] = [[]];
  for (const point of points) {
    if (point === null) runs.push([]);
    else runs[runs.length - 1].push(point);
  }
  return runs
    .filter((run) => run.length > 0)
    .map((run) => (run.length === 1 ? `M${at(run[0])}h0` : run.map((point, index) => `${index === 0 ? 'M' : 'L'}${at(point)}`).join('')))
    .join('');
}

// The SVG holds only digits, letters and a few signs, all ASCII.
const BASE64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
function base64(ascii: string): string {
  let out = '';
  for (let index = 0; index < ascii.length; index += 3) {
    const [a, b, c] = [ascii.charCodeAt(index), ascii.charCodeAt(index + 1), ascii.charCodeAt(index + 2)];
    const bits = (a << 16) | ((b || 0) << 8) | (c || 0);
    out += BASE64[(bits >> 18) & 63] + BASE64[(bits >> 12) & 63];
    out += Number.isNaN(b) ? '=' : BASE64[(bits >> 6) & 63];
    out += Number.isNaN(c) ? '=' : BASE64[bits & 63];
  }
  return out;
}

/** Three grid lines (bottom, middle, top) and one coloured line per series; the page stretches it to the plot's width. */
export function lineImage(lines: readonly (readonly LinePoint[])[]): string {
  const grid = [0, 0.5, 1].map((level) => `M0 ${down(level)}H${WIDTH}`).join('');
  const series = lines
    .map((points, index) => ({ d: pathOf(points), color: COLORS[index % COLORS.length] }))
    .filter((line) => line.d !== '')
    .map((line) => `<path d="${line.d}" stroke="${line.color}" stroke-width="2"/>`);
  const svg = [
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${WIDTH} ${HEIGHT}" preserveAspectRatio="none">`,
    `<g fill="none" stroke-linecap="round" stroke-linejoin="round">`,
    `<path d="${grid}" stroke="${GRID}" stroke-width="1"/>`,
    ...series,
    '</g></svg>',
  ].join('');
  return `data:image/svg+xml;base64,${base64(svg)}`;
}
