import * as React from 'react';
import {
  Area,
  AreaChart,
  Line,
  LineChart,
  ResponsiveContainer,
} from 'recharts';
import { cn } from '@/lib/utils';

export interface SparklineProps extends React.ComponentProps<'div'> {
  /** Series to plot, oldest first. */
  data: readonly number[];
  variant?: 'area' | 'line';
  /** Any CSS color or CSS variable. Defaults to var(--primary). */
  color?: string;
  curve?: 'monotone' | 'linear' | 'step';
  strokeWidth?: number;
  animate?: boolean;
  animationDuration?: number;
}

export function Sparkline({
  data,
  variant = 'area',
  color = 'var(--primary)',
  curve = 'monotone',
  strokeWidth = 1.5,
  animate = true,
  animationDuration = 500,
  className,
  ...props
}: SparklineProps) {
  const id = React.useId().replace(/:/g, '');
  const points = React.useMemo(() => data.map((y, i) => ({ i, y })), [data]);
  const margin = { top: 2, right: 0, bottom: 0, left: 0 };

  return (
    <div
      data-slot="sparkline"
      aria-hidden="true"
      className={cn('h-9 w-full', className)}
      {...props}
    >
      <ResponsiveContainer width="100%" height="100%">
        {variant === 'line' ? (
          <LineChart data={points} margin={margin}>
            <Line
              type={curve}
              dataKey="y"
              stroke={color}
              strokeWidth={strokeWidth}
              dot={false}
              isAnimationActive={animate}
              animationDuration={animationDuration}
              animationEasing="ease-out"
            />
          </LineChart>
        ) : (
          <AreaChart data={points} margin={margin}>
            <defs>
              <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={color} stopOpacity={0.25} />
                <stop offset="100%" stopColor={color} stopOpacity={0.0} />
              </linearGradient>
            </defs>
            <Area
              type={curve}
              dataKey="y"
              stroke={color}
              strokeWidth={strokeWidth}
              fill={`url(#${id})`}
              dot={false}
              isAnimationActive={animate}
              animationDuration={animationDuration}
              animationEasing="ease-out"
            />
          </AreaChart>
        )}
      </ResponsiveContainer>
    </div>
  );
}
