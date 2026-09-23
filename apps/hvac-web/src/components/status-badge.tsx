import type { ReactNode } from 'react';

import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

export type StatusTone = 'success' | 'warning' | 'destructive' | 'in-progress' | 'info' | 'neutral';

interface StatusBadgeProps {
  readonly children?: ReactNode;
  readonly label?: ReactNode;
  readonly tone?: StatusTone;
  readonly pulse?: boolean;
  readonly className?: string;
}

const toneClassName: Readonly<Record<StatusTone, string>> = {
  success: 'border-emerald-200 bg-emerald-50/80 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-400',
  warning: 'border-amber-200 bg-amber-50/80 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-400',
  destructive: 'border-destructive/25 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/15',
  'in-progress': 'border-sky-200 bg-sky-50/80 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400',
  info: 'border-sky-200 bg-sky-50/80 text-sky-700 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-400',
  neutral: 'border-border/80 bg-muted/30 text-muted-foreground',
};

const dotClassName: Readonly<Record<StatusTone, string>> = {
  success: 'bg-emerald-600 dark:bg-emerald-400',
  warning: 'bg-amber-500',
  destructive: 'bg-destructive',
  'in-progress': 'bg-sky-500',
  info: 'bg-sky-500',
  neutral: 'bg-muted-foreground/60',
};

/**
 * Product status semantics rendered through the official shadcn Badge primitive.
 * This component maps domain status tones only; it does not define another badge primitive.
 */
export function StatusBadge({
  children,
  label,
  tone = 'neutral',
  pulse = false,
  className,
}: StatusBadgeProps) {
  return (
    <Badge
      variant="outline"
      className={cn('gap-1.5 px-2.5 tracking-tight select-none', toneClassName[tone], className)}
    >
      {pulse ? (
        <span className="relative flex size-2 shrink-0" aria-hidden="true">
          <span className={cn('absolute inline-flex size-full animate-ping rounded-full opacity-75', dotClassName[tone])} />
          <span className={cn('relative inline-flex size-2 rounded-full', dotClassName[tone])} />
        </span>
      ) : (
        <span className={cn('size-1.5 shrink-0 rounded-full', dotClassName[tone])} aria-hidden="true" />
      )}
      <span>{children ?? label}</span>
    </Badge>
  );
}
