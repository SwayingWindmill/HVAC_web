import type { LucideIcon } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface SurfaceFactItem {
  readonly label: string;
  readonly value: string;
  readonly detail?: string;
  readonly icon?: LucideIcon;
  readonly tone?: 'neutral' | 'warning' | 'destructive';
}

interface SurfaceFactStripProps {
  readonly facts: readonly SurfaceFactItem[];
  readonly ariaLabel: string;
  readonly className?: string;
}

const toneClassName: Readonly<Record<NonNullable<SurfaceFactItem['tone']>, string>> = {
  neutral: 'text-muted-foreground',
  warning: 'text-warning',
  destructive: 'text-destructive',
};

export function SurfaceFactStrip({ facts, ariaLabel, className }: SurfaceFactStripProps) {
  return (
    <section
      className={cn('overflow-hidden rounded-lg border bg-card shadow-xs', className)}
      aria-label={ariaLabel}
    >
      <div className={cn('grid sm:grid-cols-2 xl:divide-x', facts.length >= 6 ? 'xl:grid-cols-6' : facts.length === 3 ? 'xl:grid-cols-3' : 'xl:grid-cols-4')}>
        {facts.map((fact, index) => {
          const Icon = fact.icon;
          const tone = toneClassName[fact.tone ?? 'neutral'];
          return (
            <div
              key={fact.label}
              className={cn(
                'flex min-w-0 items-center gap-3 px-4 py-3.5',
                index > 0 && 'border-t sm:border-t-0',
                index % 2 === 1 && 'sm:border-l xl:border-l-0',
                index >= 2 && 'sm:border-t xl:border-t-0',
              )}
            >
              {Icon ? (
                <span className={cn('grid size-8 shrink-0 place-items-center rounded-md bg-muted/70', tone)}>
                  <Icon className="size-4" aria-hidden="true" />
                </span>
              ) : null}
              <div className="min-w-0">
                <div className="flex min-w-0 items-baseline gap-2">
                  <strong className="text-xl font-semibold tracking-tight tabular-nums">{fact.value}</strong>
                  <span className="truncate text-xs font-medium text-muted-foreground">{fact.label}</span>
                </div>
                {fact.detail ? <p className="mt-0.5 truncate text-[11px] text-muted-foreground">{fact.detail}</p> : null}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
