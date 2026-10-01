import * as React from 'react';
import { cn } from '@/lib/utils';
import { type NumberFormat } from '@/lib/format';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { DeltaBadge, getDeltaDirection } from '@/components/ui/delta-badge';
import { Sparkline } from '@/components/ui/sparkline';
import { MetricValue } from '@/components/ui/metric-value';

export interface KpiCardProps extends Omit<
  React.ComponentProps<typeof Card>,
  'children'
> {
  /** Metric name, e.g. "今日综合能耗". */
  label: string;
  /** Current value. Numbers are formatted with `format`; strings render as-is. */
  value: number | string;
  /** Suffix text/unit after value, e.g. "kWh", "元", "kgCO₂". */
  suffix?: React.ReactNode;
  /** Fractional change vs. the previous period, e.g. -0.042 for -4.2%. */
  delta?: number;
  /** Context for the delta, e.g. "较昨日", "环比". */
  deltaLabel?: string;
  /** Series for the sparkline. Rendered when it has two or more points. */
  trend?: number[];
  /** Custom sparkline color override. */
  trendColor?: string;
  format?: NumberFormat;
  currency?: string;
  /** Treat a decrease as good and an increase as bad (energy, costs, emissions, alarms). */
  invertDelta?: boolean;
  /** Optional subtle icon shown before the label. */
  icon?: React.ReactNode;
  children?: React.ReactNode;
}

export function KpiCard({
  label,
  value,
  suffix,
  delta,
  deltaLabel,
  trend,
  trendColor: customTrendColor,
  format = 'number',
  currency,
  invertDelta = false,
  icon,
  className,
  children,
  ...props
}: KpiCardProps) {
  const direction = getDeltaDirection(delta);
  const isPositive =
    direction === 'flat' ? null : (direction === 'up') !== invertDelta;
  const autoTrendColor =
    isPositive === true
      ? 'var(--color-emerald-500, #10b981)'
      : isPositive === false
        ? 'var(--color-rose-500, #f43f5e)'
        : 'var(--primary)';
  const activeTrendColor = customTrendColor ?? autoTrendColor;

  return (
    <Card
      data-slot="kpi-card"
      data-direction={direction}
      className={cn(
        'group relative overflow-hidden transition-all duration-150 hover:border-border/90 hover:shadow-xs py-3 px-3.5 gap-2 border-border/80 bg-card',
        className,
      )}
      {...props}
    >
      <CardHeader className="p-0">
        <CardDescription className="flex items-center justify-between text-xs font-medium text-muted-foreground">
          <span className="flex items-center gap-1.5 truncate [&>svg]:size-3.5 [&>svg]:text-muted-foreground/80">
            {icon}
            <span className="truncate">{label}</span>
          </span>
          {delta !== undefined ? (
            <DeltaBadge delta={delta} invert={invertDelta} className="shrink-0 text-[11px]" />
          ) : null}
        </CardDescription>
        <CardTitle className="mt-1 flex items-baseline gap-1 text-2xl font-bold tabular-nums tracking-tight text-foreground">
          <MetricValue value={value} format={format} currency={currency} />
          {suffix ? (
            <span className="text-xs font-normal text-muted-foreground">
              {suffix}
            </span>
          ) : null}
        </CardTitle>
      </CardHeader>

      {trend && trend.length > 1 ? (
        <CardContent className="p-0 pt-0.5">
          <Sparkline data={trend} color={activeTrendColor} className="h-8" />
        </CardContent>
      ) : null}

      {deltaLabel || children ? (
        <CardContent className="p-0 pt-1 text-[11px] text-muted-foreground truncate">
          {deltaLabel ? (
            <span className="text-foreground/75 font-medium">{deltaLabel} · </span>
          ) : null}
          {children}
        </CardContent>
      ) : null}
    </Card>
  );
}
