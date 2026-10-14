import type { WorkOrderStatus } from '@/api/work-orders';
import { cn } from '@/lib/utils';

/** Status read by shape: ○ waiting, ◐ in progress, ▲ blocked, ● done, a dashed ring cancelled. */
export function StatusShape({ status, className }: { readonly status: WorkOrderStatus; readonly className?: string }) {
  return (
    <svg viewBox="0 0 10 10" className={cn('size-2.5 shrink-0', className)} aria-hidden="true">
      {status === 'BLOCKED' ? <path d="M5 0.5 9.5 9H0.5z" className="fill-warning" />
        : status === 'COMPLETED' ? <circle cx="5" cy="5" r="5" className="fill-success" />
        : status === 'IN_PROGRESS' ? (
          <>
            <circle cx="5" cy="5" r="3.8" fill="none" strokeWidth="1.5" className="stroke-primary" />
            <path d="M5 1.2a3.8 3.8 0 0 1 0 7.6z" className="fill-primary" />
          </>
        )
        : <circle cx="5" cy="5" r="3.8" fill="none" strokeWidth="1.5" strokeDasharray={status === 'CANCELLED' ? '1.6 1.4' : undefined} className="stroke-muted-foreground" />}
    </svg>
  );
}
