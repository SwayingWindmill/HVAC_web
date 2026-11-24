export type SparklineCurve = 'monotone' | 'linear' | 'step';

export interface SparklinePoint {
  readonly x: number;
  readonly y: number;
}

export interface SparklineSegment {
  readonly linePath: string;
  readonly areaPath: string;
}

export interface SparklineGeometry {
  readonly width: number;
  readonly height: number;
  readonly segments: readonly SparklineSegment[];
}

export const SPARKLINE_VIEWBOX_WIDTH = 120;
export const SPARKLINE_VIEWBOX_HEIGHT = 36;
const PAD_TOP = 3;
const PAD_BOTTOM = 2;

function round2(value: number): number {
  return Math.round(value * 100) / 100;
}

function buildLinearPath(points: readonly SparklinePoint[]): string {
  const [first, ...rest] = points;
  if (!first) return '';
  let path = `M ${round2(first.x)} ${round2(first.y)}`;
  for (const point of rest) {
    path += ` L ${round2(point.x)} ${round2(point.y)}`;
  }
  return path;
}

function buildStepPath(points: readonly SparklinePoint[]): string {
  const [first, ...rest] = points;
  if (!first) return '';
  let path = `M ${round2(first.x)} ${round2(first.y)}`;
  for (const point of rest) {
    path += ` H ${round2(point.x)} V ${round2(point.y)}`;
  }
  return path;
}

/**
 * Fritsch-Carlson monotone piecewise cubic Hermite spline serialized as SVG cubic Beziers.
 * Prevents overshoot between flat and steep spans, matching Recharts `monotone` curves.
 */
function buildMonotonePath(points: readonly SparklinePoint[]): string {
  const count = points.length;
  if (count === 0) return '';
  if (count === 1) {
    const p = points[0];
    return `M ${round2(p.x)} ${round2(p.y)}`;
  }
  if (count === 2) return buildLinearPath(points);

  const slopes: number[] = new Array(count - 1);
  const dx: number[] = new Array(count - 1);
  for (let i = 0; i < count - 1; i += 1) {
    const stepX = points[i + 1].x - points[i].x;
    dx[i] = stepX;
    slopes[i] = stepX === 0 ? 0 : (points[i + 1].y - points[i].y) / stepX;
  }

  const tangents: number[] = new Array(count);
  tangents[0] = slopes[0];
  tangents[count - 1] = slopes[count - 2];
  for (let i = 1; i < count - 1; i += 1) {
    const prev = slopes[i - 1];
    const next = slopes[i];
    if (prev * next <= 0) {
      tangents[i] = 0;
    } else {
      const prevDx = dx[i - 1];
      const nextDx = dx[i];
      const common = prevDx + nextDx;
      tangents[i] = (3 * common) / ((common + nextDx) / prev + (common + prevDx) / next);
    }
  }

  let path = `M ${round2(points[0].x)} ${round2(points[0].y)}`;
  for (let i = 0; i < count - 1; i += 1) {
    const p0 = points[i];
    const p1 = points[i + 1];
    const span = dx[i] / 3;
    const cp1x = round2(p0.x + span);
    const cp1y = round2(p0.y + tangents[i] * span);
    const cp2x = round2(p1.x - span);
    const cp2y = round2(p1.y - tangents[i + 1] * span);
    path += ` C ${cp1x} ${cp1y} ${cp2x} ${cp2y} ${round2(p1.x)} ${round2(p1.y)}`;
  }
  return path;
}

/**
 * Splits a nullable numeric series into contiguous SVG path segments without rendering nulls.
 */
export function buildSparklineGeometry(
  data: readonly (number | null)[],
  curve: SparklineCurve = 'monotone',
): SparklineGeometry {
  const count = data.length;
  let min = Infinity;
  let max = -Infinity;
  const runs: (readonly { readonly index: number; readonly value: number }[])[] = [];
  let current: { readonly index: number; readonly value: number }[] = [];

  for (let i = 0; i < count; i += 1) {
    const value = data[i];
    if (value === null || !Number.isFinite(value)) {
      if (current.length > 0) {
        runs.push(current);
        current = [];
      }
      continue;
    }
    if (value < min) min = value;
    if (value > max) max = value;
    current.push({ index: i, value });
  }
  if (current.length > 0) runs.push(current);

  if (runs.length === 0) {
    return { width: SPARKLINE_VIEWBOX_WIDTH, height: SPARKLINE_VIEWBOX_HEIGHT, segments: [] };
  }

  const plotHeight = SPARKLINE_VIEWBOX_HEIGHT - PAD_TOP - PAD_BOTTOM;
  const midY = PAD_TOP + plotHeight / 2;
  const span = max - min;
  const maxIndex = Math.max(1, count - 1);

  const segments: SparklineSegment[] = runs.map((run) => {
    const points: SparklinePoint[] = run.map(({ index, value }) => ({
      x: count === 1 ? SPARKLINE_VIEWBOX_WIDTH / 2 : (index / maxIndex) * SPARKLINE_VIEWBOX_WIDTH,
      y: span === 0 ? midY : PAD_TOP + ((max - value) / span) * plotHeight,
    }));
    const linePath =
      curve === 'linear'
        ? buildLinearPath(points)
        : curve === 'step'
          ? buildStepPath(points)
          : buildMonotonePath(points);
    const first = points[0];
    const last = points[points.length - 1];
    const areaPath =
      points.length >= 2 && first && last
        ? `${linePath} L ${round2(last.x)} ${SPARKLINE_VIEWBOX_HEIGHT} L ${round2(first.x)} ${SPARKLINE_VIEWBOX_HEIGHT} Z`
        : '';
    return { linePath, areaPath };
  });

  return { width: SPARKLINE_VIEWBOX_WIDTH, height: SPARKLINE_VIEWBOX_HEIGHT, segments };
}
