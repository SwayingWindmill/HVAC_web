import * as React from 'react';
import { cn } from '@/lib/utils';
import {
  buildSparklineGeometry,
  SPARKLINE_VIEWBOX_HEIGHT,
  SPARKLINE_VIEWBOX_WIDTH,
  type SparklineCurve,
} from './sparkline-path';

export interface SparklineProps extends React.ComponentProps<'div'> {
  /** Series to plot, oldest first. Null entries leave an honest gap across missing buckets. */
  data: readonly (number | null)[];
  variant?: 'area' | 'line';
  /** Any CSS color or CSS variable. Defaults to var(--primary). */
  color?: string;
  curve?: SparklineCurve;
  strokeWidth?: number;
  animate?: boolean;
  animationDuration?: number;
}

export const Sparkline = React.memo(function Sparkline({
  data,
  variant = 'area',
  color = 'var(--primary)',
  curve = 'monotone',
  strokeWidth = 1.5,
  animate: _animate = false,
  animationDuration: _animationDuration = 500,
  className,
  ...props
}: SparklineProps) {
  const id = React.useId().replace(/:/g, '');
  const geometry = React.useMemo(() => buildSparklineGeometry(data, curve), [data, curve]);

  return (
    <div
      data-slot="sparkline"
      aria-hidden="true"
      className={cn('h-9 w-full', className)}
      {...props}
    >
      <svg
        viewBox={`0 0 ${SPARKLINE_VIEWBOX_WIDTH} ${SPARKLINE_VIEWBOX_HEIGHT}`}
        preserveAspectRatio="none"
        className="block size-full overflow-visible"
      >
        {variant === 'area' && geometry.segments.length > 0 ? (
          <defs>
            <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.25} />
              <stop offset="100%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
        ) : null}
        {geometry.segments.map((segment, index) => (
          <React.Fragment key={index}>
            {variant === 'area' && segment.areaPath ? (
              <path d={segment.areaPath} fill={`url(#${id})`} stroke="none" />
            ) : null}
            <path
              d={segment.linePath}
              fill="none"
              stroke={color}
              strokeWidth={strokeWidth}
              strokeLinecap="round"
              strokeLinejoin="round"
              vectorEffect="non-scaling-stroke"
            />
          </React.Fragment>
        ))}
      </svg>
    </div>
  );
});
