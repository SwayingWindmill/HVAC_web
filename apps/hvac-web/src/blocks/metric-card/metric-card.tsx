import type { ReactNode } from 'react';

import { Card, CardAction, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';

interface MetricCardProps {
  readonly label: string;
  readonly value: ReactNode;
  readonly badge?: ReactNode;
  /** The first footer line: the fact that explains the value. */
  readonly lead: ReactNode;
  /** The second footer line: its time, source or a link onward. */
  readonly detail: ReactNode;
}

/** A headline figure in the dashboard-01 grammar: label, value and badge, then two footer lines. */
export function MetricCard({ label, value, badge, lead, detail }: MetricCardProps) {
  return (
    <Card className="@container/card">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">{value}</CardTitle>
        {badge ? <CardAction>{badge}</CardAction> : null}
      </CardHeader>
      <CardFooter className="flex-col items-start gap-1.5 text-sm">
        <div className="line-clamp-1 font-medium">{lead}</div>
        <div className="line-clamp-1 text-muted-foreground">{detail}</div>
      </CardFooter>
    </Card>
  );
}

/** A row of peer MetricCards; it reflows by the page container, not the viewport. */
export function MetricGrid({ ariaLabel, children, className }: { readonly ariaLabel: string; readonly children: ReactNode; readonly className?: string }) {
  return (
    <section
      aria-label={ariaLabel}
      className={cn('grid grid-cols-1 gap-4 *:data-[slot=card]:shadow-xs @xl/main:grid-cols-2 @5xl/main:grid-cols-4', className)}
    >
      {children}
    </section>
  );
}

/** A figure with its unit; an absent figure says so instead of showing zero, a stale one is dimmed. */
export function MetricValue({ value, unit, stale = false }: { readonly value: string | null; readonly unit?: string; readonly stale?: boolean }) {
  if (value === null) return <span className="text-muted-foreground">暂无数据</span>;
  return (
    <span className={cn(stale && 'text-muted-foreground')} title={stale ? '数据过期或质量降级' : undefined}>
      {value}
      {unit ? <span className="ml-1.5 text-sm font-normal text-muted-foreground">{unit}</span> : null}
    </span>
  );
}
