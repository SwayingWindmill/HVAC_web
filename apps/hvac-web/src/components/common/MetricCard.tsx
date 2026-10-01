import type { ReactNode } from 'react';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { TrendingDown, TrendingUp, Minus } from 'lucide-react';

export interface MetricTrend {
  readonly value: string | number;
  readonly direction?: 'up' | 'down' | 'flat' | 'neutral';
  readonly label?: string;
  readonly isGood?: boolean; // If true, up is green (or down is green depending on context)
}

export interface MetricCardProps {
  readonly title: ReactNode;
  readonly value: ReactNode;
  readonly unit?: string;
  readonly icon?: ReactNode;
  readonly badge?: ReactNode;
  readonly trend?: MetricTrend;
  readonly subtext?: ReactNode;
  readonly chartSlot?: ReactNode;
  readonly footer?: ReactNode;
  readonly loading?: boolean;
  readonly className?: string;
}

export function MetricCard({
  title,
  value,
  unit,
  icon,
  badge,
  trend,
  subtext,
  chartSlot,
  footer,
  loading = false,
  className,
}: MetricCardProps) {
  if (loading) {
    return (
      <Card className={cn('p-5 space-y-3', className)}>
        <div className="flex justify-between items-center">
          <Skeleton className="h-4 w-24" />
          <Skeleton className="size-5 rounded" />
        </div>
        <Skeleton className="h-8 w-32" />
        <Skeleton className="h-3 w-40" />
      </Card>
    );
  }

  return (
    <Card className={cn('overflow-hidden transition-colors border-border/80 shadow-xs', className)}>
      <CardHeader className="flex flex-row items-center justify-between pb-2 p-5">
        <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          {title}
        </span>
        <div className="flex items-center gap-1.5">
          {badge}
          {icon && <div className="text-muted-foreground/80">{icon}</div>}
        </div>
      </CardHeader>

      <CardContent className="p-5 pt-0 space-y-2">
        <div className="flex items-baseline gap-1.5">
          <span className="text-2xl font-bold tracking-tight text-foreground">
            {value}
          </span>
          {unit && (
            <span className="text-xs font-normal text-muted-foreground">
              {unit}
            </span>
          )}
        </div>

        {trend && (
          <div className="flex items-center gap-1.5 text-xs">
            <span
              className={cn(
                'inline-flex items-center gap-0.5 font-medium px-1.5 py-0.5 rounded-sm',
                trend.isGood === undefined
                  ? 'bg-muted text-muted-foreground'
                  : trend.isGood
                    ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-400'
                    : 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-400'
              )}
            >
              {trend.direction === 'up' && <TrendingUp className="size-3" />}
              {trend.direction === 'down' && <TrendingDown className="size-3" />}
              {(trend.direction === 'flat' || trend.direction === 'neutral') && <Minus className="size-3" />}
              {trend.value}
            </span>
            {trend.label && (
              <span className="text-muted-foreground">{trend.label}</span>
            )}
          </div>
        )}

        {subtext && <div className="text-xs text-muted-foreground">{subtext}</div>}
        {chartSlot && <div className="pt-2">{chartSlot}</div>}
        {footer && <div className="pt-3 border-t border-border/40 text-xs">{footer}</div>}
      </CardContent>
    </Card>
  );
}
