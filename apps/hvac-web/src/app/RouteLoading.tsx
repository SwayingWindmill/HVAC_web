import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';

interface RouteLoadingProps {
  label: string;
  testId?: string;
  routeState?: string;
  siteId?: string;
  variant?: 'route' | 'shell';
}

/** A page-shaped skeleton while a route or the shell loads: title, a figure strip and a panel of rows. */
export function RouteLoading({
  label,
  testId,
  routeState,
  siteId,
  variant = 'route',
}: RouteLoadingProps) {
  return (
    <section
      className={cn('flex flex-col gap-[18px] px-7 pt-[22px] pb-6', variant === 'shell' && 'min-h-svh bg-background')}
      data-testid={testId}
      data-route-state={routeState}
      data-site-id={siteId}
    >
      <span className="sr-only" role="status" aria-live="polite">{label}</span>
      <div className="flex flex-col gap-2" aria-hidden="true">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-4 w-24" />
      </div>
      <div className="grid grid-cols-4 gap-4 rounded-xl border bg-card p-5" aria-hidden="true">
        {Array.from({ length: 4 }, (_, index) => (
          <div key={index} className="flex flex-col gap-2">
            <Skeleton className="h-3 w-16" />
            <Skeleton className="h-7 w-24" />
          </div>
        ))}
      </div>
      <div className="flex flex-col gap-3 rounded-xl border bg-card p-5" aria-hidden="true">
        <Skeleton className="h-4 w-32" />
        {Array.from({ length: 6 }, (_, index) => <Skeleton key={index} className="h-9" />)}
      </div>
    </section>
  );
}
