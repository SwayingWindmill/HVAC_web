import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

/** A label over its value, for facts laid out in a grid rather than a list. */
export function FactTile({ label, children, className }: { readonly label: ReactNode; readonly children: ReactNode; readonly className?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={cn('font-medium tabular-nums', className)}>{children}</dd>
    </div>
  );
}
