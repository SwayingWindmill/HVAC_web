import type { LucideIcon } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface IndependentStateItem {
  readonly label: string;
  readonly value: string;
  readonly detail?: string;
  readonly icon?: LucideIcon;
  readonly tone?: 'neutral' | 'warning' | 'destructive';
}

interface IndependentStateStripProps {
  readonly items: readonly IndependentStateItem[];
  readonly ariaLabel: string;
  readonly className?: string;
}

const dotClassName: Readonly<Record<NonNullable<IndependentStateItem['tone']>, string>> = {
  neutral: 'bg-muted-foreground/45',
  warning: 'bg-warning',
  destructive: 'bg-destructive',
};

export function IndependentStateStrip({ items, ariaLabel, className }: IndependentStateStripProps) {
  return (
    <section className={cn('overflow-hidden rounded-lg border bg-card shadow-xs', className)} aria-label={ariaLabel}>
      <div className="grid sm:grid-cols-2 xl:grid-cols-4 xl:divide-x">
        {items.map((item, index) => {
          const Icon = item.icon;
          const tone = item.tone ?? 'neutral';
          return (
            <div
              key={item.label}
              className={cn(
                'flex min-w-0 items-center gap-3 px-4 py-3',
                index > 0 && 'border-t sm:border-t-0',
                index % 2 === 1 && 'sm:border-l xl:border-l-0',
                index >= 2 && 'sm:border-t xl:border-t-0',
              )}
            >
              {Icon ? (
                <span className="grid size-8 shrink-0 place-items-center rounded-md bg-muted/70 text-muted-foreground">
                  <Icon className="size-4" aria-hidden="true" />
                </span>
              ) : <span className={cn('size-2 shrink-0 rounded-full', dotClassName[tone])} aria-hidden="true" />}
              <div className="min-w-0">
                <p className="text-[11px] text-muted-foreground">{item.label}</p>
                <strong className={cn('mt-0.5 block truncate text-sm font-semibold', tone === 'warning' && 'text-warning', tone === 'destructive' && 'text-destructive')}>{item.value}</strong>
                {item.detail ? <p className="mt-0.5 truncate text-[11px] text-muted-foreground">{item.detail}</p> : null}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
