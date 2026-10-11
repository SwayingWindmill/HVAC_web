import type { ReactNode } from 'react';

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

/** One headline figure in a MetricGrid strip: label, value with its badge, then what it means and where it comes from. */
export function MetricCard({ label, value, badge, lead, detail }: MetricCardProps) {
  return (
    <div data-slot="metric" className="flex min-w-0 flex-col gap-3 bg-card px-5 py-4">
      <div className="truncate text-sm text-muted-foreground">{label}</div>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="truncate text-[1.75rem] leading-none font-medium tracking-tight tabular-nums">{value}</div>
        {badge ? <div className="shrink-0">{badge}</div> : null}
      </div>
      <div className="flex min-w-0 flex-col gap-0.5 text-xs">
        <div className="line-clamp-1 text-foreground/80">{lead}</div>
        <div className="line-clamp-1 text-muted-foreground">{detail}</div>
      </div>
    </div>
  );
}

/**
 * Peer MetricCards as one strip: a single bordered surface split by hairlines, so a row of
 * figures reads as one statement about the page rather than as separate tiles. It reflows by
 * the page container, not the viewport.
 */
export function MetricGrid({ ariaLabel, children, className }: { readonly ariaLabel: string; readonly children: ReactNode; readonly className?: string }) {
  return (
    <section
      aria-label={ariaLabel}
      className={cn('grid grid-cols-1 gap-px overflow-hidden rounded-xl border bg-border shadow-xs @xl/main:grid-cols-2 @5xl/main:grid-cols-4', className)}
    >
      {children}
    </section>
  );
}

/** A figure with its unit; an absent figure says so instead of showing zero, a stale one is dimmed. */
export function MetricValue({ value, unit, stale = false }: { readonly value: string | null; readonly unit?: string; readonly stale?: boolean }) {
  if (value === null) return <span className="text-lg text-muted-foreground">暂无数据</span>;
  return (
    <span className={cn(stale && 'text-muted-foreground')} title={stale ? '数据过期或质量降级' : undefined}>
      {value}
      {unit ? <span className="ml-1 text-sm font-normal tracking-normal text-muted-foreground">{unit}</span> : null}
    </span>
  );
}
