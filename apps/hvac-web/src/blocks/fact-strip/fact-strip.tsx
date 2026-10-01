import type { CSSProperties, ReactNode } from 'react';

import { cn } from '@/lib/utils';

export type FactStripTone = 'default' | 'accent' | 'positive' | 'warning' | 'critical';

export interface FactStripItem {
  readonly key?: string;
  readonly label: ReactNode;
  readonly value: ReactNode;
  readonly suffix?: ReactNode;
  readonly detail?: ReactNode;
  readonly icon?: ReactNode;
  readonly tone?: FactStripTone;
}

interface FactStripProps {
  readonly items: readonly FactStripItem[];
  readonly ariaLabel?: string;
  readonly className?: string;
}

const toneClassName: Readonly<Record<FactStripTone, string>> = {
  default: 'text-foreground',
  accent: 'text-primary',
  positive: 'text-emerald-700 dark:text-emerald-400',
  warning: 'text-amber-700 dark:text-amber-400',
  critical: 'text-destructive',
};

export function FactStrip({ items, ariaLabel = '关键事实', className }: FactStripProps) {
  return (
    <section
      aria-label={ariaLabel}
      className={cn('grid gap-px overflow-hidden rounded-lg border bg-border sm:grid-cols-2 xl:grid-cols-[repeat(var(--fact-count),minmax(0,1fr))]', className)}
      style={{ '--fact-count': Math.max(1, items.length) } as CSSProperties}
    >
      {items.map((item, index) => {
        const tone = item.tone ?? 'default';
        return (
          <div
            key={item.key ?? `${index}-${String(item.label)}`}
            className="min-w-0 bg-card px-4 py-3.5"
          >
            <div className="flex min-w-0 items-center justify-between gap-3">
              <span className="truncate text-xs font-medium text-muted-foreground">{item.label}</span>
              {item.icon ? <span className={cn('shrink-0 [&>svg]:size-4', toneClassName[tone])} aria-hidden="true">{item.icon}</span> : null}
            </div>
            <div className="mt-1.5 flex min-w-0 items-baseline gap-1.5">
              <strong className={cn('truncate text-xl font-semibold tracking-tight tabular-nums', toneClassName[tone])}>{item.value}</strong>
              {item.suffix ? <span className="shrink-0 text-xs font-medium text-muted-foreground">{item.suffix}</span> : null}
            </div>
            {item.detail ? <div className="mt-1 truncate text-xs text-muted-foreground">{item.detail}</div> : null}
          </div>
        );
      })}
    </section>
  );
}
