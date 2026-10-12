import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

/** Label–value rows split by hairlines: the facts behind a card's headline. */
export function FactList({ children, className }: { readonly children: ReactNode; readonly className?: string }) {
  return <dl className={cn('divide-y', className)}>{children}</dl>;
}

export function FactRow({ label, children, detail }: { readonly label: ReactNode; readonly children: ReactNode; readonly detail?: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-6 py-2.5 first:pt-0 last:pb-0">
      <dt className="shrink-0 text-sm text-muted-foreground">{label}</dt>
      <dd className="flex min-w-0 flex-col items-end gap-0.5 text-right">
        <span className="text-sm font-medium tabular-nums">{children}</span>
        {detail ? <span className="text-xs text-muted-foreground">{detail}</span> : null}
      </dd>
    </div>
  );
}
